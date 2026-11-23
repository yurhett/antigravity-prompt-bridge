package rewrite

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/file"
	"github.com/dop251/goja/parser"
)

type sourceEdit struct {
	start, end int
	value      string
}
type jsShadow struct {
	name       string
	start, end file.Idx
}
type jsAnalysis struct {
	source   string
	function *ast.FunctionLiteral
	shadows  []jsShadow
	nodes    int
}

const jsWrapper = "async function __agent_bridge__(){\n"

func parseAgentJavaScript(code string) (result *jsAnalysis, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = fmt.Errorf("code tool JavaScript exceeds parser limits")
		}
	}()
	if len(code) > maxAliasStreamBytes {
		return nil, fmt.Errorf("code tool exceeds buffer limit")
	}
	program, e := parser.ParseFile(nil, "agent-code.js", jsWrapper+code+"\n}", 0, parser.WithDisableSourceMaps)
	if e != nil {
		return nil, fmt.Errorf("code tool JavaScript cannot be parsed safely")
	}
	f := program.Body[0].(*ast.FunctionDeclaration).Function
	a := &jsAnalysis{source: code, function: f}
	var scan func(ast.Node, ast.Node, ast.Node, int) error
	bind := func(target ast.Expression, scope ast.Node) {
		for _, name := range jsBindingNames(target) {
			a.shadows = append(a.shadows, jsShadow{name, scope.Idx0(), scope.Idx1()})
		}
	}
	scan = func(n, block, fn ast.Node, depth int) error {
		if depth > 512 || a.nodes > 100000 {
			return fmt.Errorf("code tool AST limit")
		}
		a.nodes++
		switch v := n.(type) {
		case *ast.FunctionDeclaration:
			if v.Function.Name != nil {
				bind(v.Function.Name, block)
			}
		case *ast.ClassDeclaration:
			if v.Class.Name != nil {
				bind(v.Class.Name, block)
			}
		case *ast.FunctionLiteral:
			block, fn = n, n
			if v.Name != nil {
				bind(v.Name, n)
			}
			for _, p := range v.ParameterList.List {
				bind(p.Target, n)
			}
			if v.ParameterList.Rest != nil {
				bind(v.ParameterList.Rest, n)
			}
		case *ast.ArrowFunctionLiteral:
			block, fn = n, n
			for _, p := range v.ParameterList.List {
				bind(p.Target, n)
			}
			if v.ParameterList.Rest != nil {
				bind(v.ParameterList.Rest, n)
			}
		case *ast.BlockStatement, *ast.ForStatement, *ast.ForInStatement, *ast.ForOfStatement, *ast.SwitchStatement:
			block = n
		case *ast.CatchStatement:
			if v.Parameter != nil {
				bind(v.Parameter, n)
			}
		case *ast.VariableStatement:
			for _, p := range v.List {
				bind(p.Target, fn)
			}
		case *ast.ForLoopInitializerVarDeclList:
			for _, p := range v.List {
				bind(p.Target, fn)
			}
		case *ast.ForIntoVar:
			bind(v.Binding.Target, fn)
		case *ast.LexicalDeclaration:
			for _, p := range v.List {
				bind(p.Target, block)
			}
		case *ast.ForDeclaration:
			bind(v.Target, block)
		}
		return astChildren(n, func(child ast.Node) error { return scan(child, block, fn, depth+1) })
	}
	if err = scan(f, f, f, 0); err != nil {
		return nil, err
	}
	return a, nil
}
func jsBindingNames(expr ast.Expression) []string {
	switch e := expr.(type) {
	case *ast.Identifier:
		return []string{e.Name.String()}
	case *ast.ArrayPattern:
		var names []string
		for _, x := range e.Elements {
			names = append(names, jsBindingNames(x)...)
		}
		return append(names, jsBindingNames(e.Rest)...)
	case *ast.ObjectPattern:
		var names []string
		for _, p := range e.Properties {
			switch v := p.(type) {
			case *ast.PropertyShort:
				names = append(names, v.Name.Name.String())
			case *ast.PropertyKeyed:
				names = append(names, jsBindingNames(v.Value)...)
			}
		}
		return append(names, jsBindingNames(e.Rest)...)
	case *ast.AssignExpression:
		return jsBindingNames(e.Left)
	case *ast.SpreadElement:
		return jsBindingNames(e.Expression)
	}
	return nil
}
func astChildren(n ast.Node, visit func(ast.Node) error) error {
	var walk func(reflect.Value) error
	walk = func(v reflect.Value) error {
		if !v.IsValid() {
			return nil
		}
		if v.Kind() == reflect.Interface {
			if v.IsNil() {
				return nil
			}
			return walk(v.Elem())
		}
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return nil
			}
			if node, ok := v.Interface().(ast.Node); ok {
				return visit(node)
			}
			return nil
		}
		if v.Kind() == reflect.Slice {
			for i := 0; i < v.Len(); i++ {
				if err := walk(v.Index(i)); err != nil {
					return err
				}
			}
		}
		if v.Kind() == reflect.Struct {
			if v.CanAddr() {
				if node, ok := v.Addr().Interface().(ast.Node); ok {
					return visit(node)
				}
			}
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).CanInterface() {
					if err := walk(v.Field(i)); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	v := reflect.ValueOf(n).Elem()
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if name == "DeclarationList" {
			continue
		}
		if err := walk(v.Field(i)); err != nil {
			return err
		}
	}
	return nil
}
func (a *jsAnalysis) runtime(name string, at file.Idx) bool {
	for _, s := range a.shadows {
		if s.name == name && at >= s.start && at < s.end {
			return false
		}
	}
	return true
}
func unwrapJS(e ast.Expression) ast.Expression {
	for {
		switch v := e.(type) {
		case *ast.Optional:
			e = v.Expression
		case *ast.OptionalChain:
			e = v.Expression
		default:
			return e
		}
	}
}

type jsMember struct {
	node    ast.Node
	name    string
	literal bool
}

func jsToolMembers(e ast.Expression) ([]jsMember, *ast.Identifier, bool) {
	e = unwrapJS(e)
	switch v := e.(type) {
	case *ast.Identifier:
		return nil, v, true
	case *ast.DotExpression:
		parts, root, static := jsToolMembers(v.Left)
		return append(parts, jsMember{&v.Identifier, v.Identifier.Name.String(), false}), root, static
	case *ast.BracketExpression:
		parts, root, static := jsToolMembers(v.Left)
		if s, ok := v.Member.(*ast.StringLiteral); ok {
			return append(parts, jsMember{s, s.Value.String(), true}), root, static
		}
		return parts, root, false
	}
	return nil, nil, false
}
func (a *jsAnalysis) rewrite(plan *openCodePlan, reverse bool) (string, error) {
	var edits []sourceEdit
	add := func(node ast.Node, text string, literal bool) {
		start, end := int(node.Idx0())-1-len(jsWrapper), int(node.Idx1())-1-len(jsWrapper)
		if start < 0 || end > len(a.source) || start > end {
			return
		}
		if literal {
			raw, _ := json.Marshal(text)
			text = string(raw)
		}
		edits = append(edits, sourceEdit{start, end, text})
	}
	allowedRoots := map[file.Idx]bool{}
	var walk func(ast.Node, ast.Node) error
	walk = func(n, parent ast.Node) error {
		switch v := n.(type) {
		case *ast.Identifier:
			name := v.Name.String()
			if (name == "tools" || name == "search") && a.runtime(name, v.Idx) && !allowedRoots[v.Idx] {
				property := false
				switch p := parent.(type) {
				case *ast.DotExpression:
					property = p.Identifier.Idx == v.Idx
				case *ast.PropertyKeyed:
					property = !p.Computed && p.Key == v
				}
				if !property {
					return fmt.Errorf("code tool runtime binding alias is unsupported")
				}
			}
		case *ast.WithStatement:
			return fmt.Errorf("code tool dynamic scope is unsupported")
		case *ast.DotExpression, *ast.BracketExpression, *ast.OptionalChain:
			parts, root, static := jsToolMembers(v.(ast.Expression))
			if root != nil && root.Name.String() == "globalThis" && a.runtime("globalThis", root.Idx) && len(parts) > 0 && parts[0].name == "tools" {
				return fmt.Errorf("code tool globalThis tool access is unsupported")
			}
			if root != nil && root.Name.String() == "tools" && a.runtime("tools", root.Idx) {
				allowedRoots[root.Idx] = true
				if !static {
					return fmt.Errorf("code tool dynamic tool path is unsupported")
				}
				if len(parts) > 0 {
					ns := parts[0].name
					if direct, known := plan.tool(aliasToolKey("", ns), reverse); known {
						if direct != ns {
							add(parts[0].node, direct, parts[0].literal)
						}
						break
					}
					next, known := plan.namespace(ns, reverse)
					if known && next != ns {
						add(parts[0].node, next, parts[0].literal)
					}
					if len(parts) > 1 {
						oldPath := ns + "\x00" + parts[1].name
						nextName, exists := plan.tool(oldPath, reverse)
						if reverse && known && next != ns && !exists {
							return fmt.Errorf("code tool path was not declared or discovered")
						}
						if exists && nextName != parts[1].name {
							add(parts[1].node, nextName, parts[1].literal)
						}
					}
				}
			}
		case *ast.CallExpression:
			if id, ok := unwrapJS(v.Callee).(*ast.Identifier); ok && id.Name.String() == "eval" && a.runtime("eval", id.Idx) {
				return fmt.Errorf("code tool eval is unsupported for name restoration")
			}
			if id, ok := unwrapJS(v.Callee).(*ast.Identifier); ok && id.Name.String() == "search" && a.runtime("search", id.Idx) {
				allowedRoots[id.Idx] = true
				if len(v.ArgumentList) > 0 {
					obj, ok := v.ArgumentList[0].(*ast.ObjectLiteral)
					if !ok {
						return fmt.Errorf("code tool dynamic search options are unsupported")
					}
					for _, p := range obj.Value {
						kv, ok := p.(*ast.PropertyKeyed)
						if !ok {
							return fmt.Errorf("code tool dynamic search options are unsupported")
						}
						key, static := jsPropertyName(kv.Key)
						if !static || kv.Computed {
							return fmt.Errorf("code tool computed search option is unsupported")
						}
						if key == "namespace" {
							value, ok := kv.Value.(*ast.StringLiteral)
							if !ok {
								return fmt.Errorf("code tool dynamic namespace is unsupported")
							}
							if next, known := plan.namespace(value.Value.String(), reverse); known && next != value.Value.String() {
								add(value, next, true)
							}
						}
					}
				}
			}
		}
		return astChildren(n, func(child ast.Node) error { return walk(child, n) })
	}
	if err := walk(a.function.Body, nil); err != nil {
		return "", err
	}
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].start == edits[j].start {
			return edits[i].end < edits[j].end
		}
		return edits[i].start < edits[j].start
	})
	var result strings.Builder
	last := 0
	for _, e := range edits {
		if e.start < last {
			continue
		}
		result.WriteString(a.source[last:e.start])
		result.WriteString(e.value)
		last = e.end
	}
	result.WriteString(a.source[last:])
	return result.String(), nil
}
func jsPropertyName(e ast.Expression) (string, bool) {
	switch v := e.(type) {
	case *ast.Identifier:
		return v.Name.String(), true
	case *ast.StringLiteral:
		return v.Value.String(), true
	}
	return "", false
}

// A projection records only statically proven search-result locations. It never
// infers a catalog from a JSON shape, a filename, or a string in a tool result.
type catalogProjection struct {
	Leaf     bool
	Fields   map[string]*catalogProjection
	Elements map[int]*catalogProjection
}

func (a *jsAnalysis) catalogProjection() *catalogProjection {
	bindings := map[string]ast.Expression{}
	invalid := map[string]bool{}
	var rootName func(ast.Expression) string
	rootName = func(e ast.Expression) string {
		switch v := unwrapJS(e).(type) {
		case *ast.Identifier:
			return v.Name.String()
		case *ast.DotExpression:
			return rootName(v.Left)
		case *ast.BracketExpression:
			return rootName(v.Left)
		}
		return ""
	}
	var invalidate func(ast.Expression)
	invalidate = func(e ast.Expression) {
		if e == nil {
			return
		}
		switch v := e.(type) {
		case *ast.Identifier:
			invalid[v.Name.String()] = true
		default:
			_ = astChildren(e, func(n ast.Node) error {
				if x, ok := n.(ast.Expression); ok {
					invalidate(x)
				}
				return nil
			})
		}
	}
	var collect func(ast.Node)
	collect = func(n ast.Node) {
		switch v := n.(type) {
		case *ast.FunctionLiteral, *ast.ArrowFunctionLiteral:
			return
		case *ast.Binding:
			if id, ok := v.Target.(*ast.Identifier); ok {
				key := id.Name.String()
				if _, exists := bindings[key]; exists {
					invalid[key] = true
				}
				bindings[key] = v.Initializer
			}
		case *ast.AssignExpression:
			if key := rootName(v.Left); key != "" {
				invalid[key] = true
			}
		case *ast.UnaryExpression:
			if key := rootName(v.Operand); key != "" {
				invalid[key] = true
			}
		case *ast.CallExpression:
			switch receiver := unwrapJS(v.Callee).(type) {
			case *ast.DotExpression:
				if key := rootName(receiver.Left); key != "" {
					invalid[key] = true
				}
			case *ast.BracketExpression:
				if key := rootName(receiver.Left); key != "" {
					invalid[key] = true
				}
			}
			for _, arg := range v.ArgumentList {
				invalidate(arg)
			}
		}
		_ = astChildren(n, func(c ast.Node) error { collect(c); return nil })
	}
	collect(a.function.Body)
	for pass := 0; pass < len(bindings); pass++ {
		for name, e := range bindings {
			if invalid[name] {
				invalidate(e)
			}
		}
	}
	var project func(ast.Expression, map[string]bool, int) *catalogProjection
	project = func(e ast.Expression, seen map[string]bool, depth int) *catalogProjection {
		if depth > 32 || e == nil {
			return nil
		}
		e = unwrapJS(e)
		switch v := e.(type) {
		case *ast.AwaitExpression:
			return project(v.Argument, seen, depth+1)
		case *ast.Identifier:
			key := v.Name.String()
			if seen[key] || invalid[key] {
				return nil
			}
			seen2 := map[string]bool{}
			for k, x := range seen {
				seen2[k] = x
			}
			seen2[key] = true
			return project(bindings[key], seen2, depth+1)
		case *ast.CallExpression:
			if id, ok := unwrapJS(v.Callee).(*ast.Identifier); ok && id.Name.String() == "search" && a.runtime("search", id.Idx) {
				return &catalogProjection{Leaf: true}
			}
		case *ast.ObjectLiteral:
			p := &catalogProjection{Fields: map[string]*catalogProjection{}}
			for _, prop := range v.Value {
				switch kv := prop.(type) {
				case *ast.PropertyKeyed:
					if !kv.Computed {
						if key, ok := jsPropertyName(kv.Key); ok {
							if child := project(kv.Value, seen, depth+1); child != nil {
								p.Fields[key] = child
							}
						}
					}
				case *ast.PropertyShort:
					if child := project(&kv.Name, seen, depth+1); child != nil {
						p.Fields[kv.Name.Name.String()] = child
					}
				}
			}
			if len(p.Fields) > 0 {
				return p
			}
		case *ast.ArrayLiteral:
			p := &catalogProjection{Elements: map[int]*catalogProjection{}}
			for i, x := range v.Value {
				if child := project(x, seen, depth+1); child != nil {
					p.Elements[i] = child
				}
			}
			if len(p.Elements) > 0 {
				return p
			}
		}
		return nil
	}
	var returns []*catalogProjection
	var find func(ast.Node)
	find = func(n ast.Node) {
		switch v := n.(type) {
		case *ast.FunctionLiteral, *ast.ArrowFunctionLiteral:
			return
		case *ast.ReturnStatement:
			returns = append(returns, project(v.Argument, map[string]bool{}, 0))
			return
		}
		_ = astChildren(n, func(c ast.Node) error { find(c); return nil })
	}
	find(a.function.Body)
	if len(returns) == 0 && len(a.function.Body.List) > 0 {
		if e, ok := a.function.Body.List[len(a.function.Body.List)-1].(*ast.ExpressionStatement); ok {
			return project(e.Expression, map[string]bool{}, 0)
		}
	}
	// Multiple conditional returns require identical provenance, not a union.
	if len(returns) > 0 {
		first, _ := json.Marshal(returns[0])
		for _, p := range returns[1:] {
			next, _ := json.Marshal(p)
			if string(next) != string(first) {
				return nil
			}
		}
		return returns[0]
	}
	return nil
}
