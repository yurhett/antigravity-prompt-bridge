package rewrite

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dop251/goja/ast"

	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

type openCodeAdapter struct{}

func (openCodeAdapter) ID() string { return "opencode-code-mode" }
func (openCodeAdapter) Match(v protocol.AgentRequestView) bool {
	hasCatalog := false
	for _, s := range v.System {
		if codeModeCatalog(s) != "" {
			hasCatalog = true
			break
		}
	}
	if !hasCatalog {
		return false
	}
	for _, tool := range v.Tools {
		if isCodeModeExecute(tool) {
			return true
		}
	}
	return false
}

func isCodeModeExecute(tool protocol.AgentToolDeclaration) bool {
	return tool.Name == "execute" && strings.Contains(tool.Description, "confined Code Mode runtime")
}

const codeModeCatalogHeading = "## Code Mode tools (call only inside `execute`)"

func codeModeCatalog(text string) string {
	if !strings.Contains(text, codeModeCatalogHeading) || !strings.Contains(text, "# Code Mode") {
		return ""
	}
	protection := agentPromptProtection(text)
	start := -1
	hasMode := false
	for _, line := range tracePromptLines(text) {
		level := promptHeadingLevel(line.text)
		if level == 0 || protection.protectedBlock(line.start, line.end) {
			continue
		}
		if start >= 0 {
			if level <= 2 {
				return text[start:line.start]
			}
			continue
		}
		title := strings.TrimRight(line.text, " \t")
		if title == "# Code Mode" {
			hasMode = true
		} else if hasMode && title == codeModeCatalogHeading {
			start = line.end
		} else if level == 1 {
			hasMode = false
		}
	}
	if start >= 0 {
		return text[start:]
	}
	return ""
}

type openCodePlan struct {
	namespaces        map[string]string // original -> upstream
	reverseNamespaces map[string]string
	tools             map[string]string // original namespace + NUL + original member -> upstream member
	reverseTools      map[string]string
}

func (p *openCodePlan) Claims() []string {
	return []string{"tool:execute/code", "opencode-code-mode/catalog-paths"}
}
func (p *openCodePlan) SystemIdentifiers() [][2]string {
	var pairs [][2]string
	for key, member := range p.tools {
		ns, name, _ := strings.Cut(key, "\x00")
		if ns == "" {
			for _, tool := range codeToolMemberAliases(name, member) {
				pairs = append(pairs, [2]string{"tools" + tool[0], "tools" + tool[1]})
			}
			continue
		}
		for _, namespace := range codeToolMemberAliases(ns, p.namespaces[ns]) {
			for _, tool := range codeToolMemberAliases(name, member) {
				pairs = append(pairs, [2]string{"tools" + namespace[0] + tool[0], "tools" + namespace[1] + tool[1]})
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return len(pairs[i][0]) > len(pairs[j][0]) })
	return pairs
}
func (p *openCodePlan) namespace(s string, reverse bool) (string, bool) {
	if reverse {
		v, ok := p.reverseNamespaces[s]
		return v, ok
	}
	v, ok := p.namespaces[s]
	return v, ok
}
func (p *openCodePlan) tool(s string, reverse bool) (string, bool) {
	if reverse {
		v, ok := p.reverseTools[s]
		return v, ok
	}
	v, ok := p.tools[s]
	return v, ok
}

var codeModeDeclaration = regexp.MustCompile(`(?m)^[\t ]*-[\t ]+(tools(?:\.[A-Za-z_$][A-Za-z0-9_$]*|\["[^"\n]+"\]|\['[^'\n]+'\]){1,2})[\t ]*\(`)

func staticCodeToolPath(s string) ([]string, error) {
	a, err := parseAgentJavaScript("return " + s + ";")
	if err != nil {
		return nil, err
	}
	if len(a.function.Body.List) != 1 {
		return nil, fmt.Errorf("invalid catalog tool path")
	}
	r, ok := a.function.Body.List[0].(*ast.ReturnStatement)
	if !ok {
		return nil, fmt.Errorf("invalid catalog tool path")
	}
	parts, root, static := jsToolMembers(r.Argument)
	if !static || root == nil || root.Name.String() != "tools" || len(parts) < 1 || len(parts) > 2 {
		return nil, fmt.Errorf("invalid catalog tool path")
	}
	for _, part := range parts {
		if part.name == "" {
			return nil, fmt.Errorf("invalid empty catalog tool member")
		}
	}
	if len(parts) == 1 {
		return []string{parts[0].name}, nil
	}
	return []string{parts[0].name, parts[1].name}, nil
}
func (openCodeAdapter) BuildPlan(v protocol.AgentRequestView, profile *agentProfile) (AgentAdapterPlan, error) {
	valid := false
	for _, tool := range v.Tools {
		if isCodeModeExecute(tool) && hasStringParameter(tool, "code") {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("OpenCode Code Mode execute schema is unsupported")
	}
	p := &openCodePlan{namespaces: map[string]string{}, reverseNamespaces: map[string]string{}, tools: map[string]string{}, reverseTools: map[string]string{}}
	register := func(path string) error {
		parts, err := staticCodeToolPath(path)
		if err != nil {
			return err
		}
		ns, name := "", parts[0]
		if len(parts) == 2 {
			ns, name = parts[0], parts[1]
		}
		ans, aname := profile.aliasName(ns), profile.aliasName(name)
		if ns == "" {
			_, clientNamespace := p.namespaces[name]
			_, upstreamNamespace := p.reverseNamespaces[aname]
			if clientNamespace || upstreamNamespace {
				return fmt.Errorf("Code Mode root tool %q overlaps an ambiguous namespace", name)
			}
		} else {
			_, clientRoot := p.tools[aliasToolKey("", ns)]
			_, upstreamRoot := p.reverseTools[aliasToolKey("", ans)]
			if clientRoot || upstreamRoot {
				return fmt.Errorf("Code Mode namespace %q overlaps an ambiguous root tool", ns)
			}
			if old, ok := p.reverseNamespaces[ans]; ok && old != ns {
				return fmt.Errorf("Code Mode namespace alias %q is ambiguous", ans)
			}
			p.namespaces[ns] = ans
			p.reverseNamespaces[ans] = ns
		}
		key := aliasToolKey(ans, aname)
		if old, ok := p.reverseTools[key]; ok && old != name {
			return fmt.Errorf("Code Mode tool alias %q is ambiguous", path)
		}
		p.tools[aliasToolKey(ns, name)] = aname
		p.reverseTools[key] = name
		return nil
	}
	for _, text := range v.System {
		catalog := codeModeCatalog(text)
		if catalog == "" {
			continue
		}
		protection := agentPromptProtection(catalog)
		for _, match := range codeModeDeclaration.FindAllStringSubmatchIndex(catalog, -1) {
			if protection.protectedBlock(match[2], match[3]) {
				continue
			}
			if err := register(catalog[match[2]:match[3]]); err != nil {
				return nil, err
			}
		}
	}
	if len(p.tools) == 0 {
		return nil, fmt.Errorf("OpenCode Code Mode catalog has no supported declarations")
	}
	// Dynamic discovery can add names only through a proven search-return slot.
	proofs, err := p.catalogProofs(v)
	if err != nil {
		return nil, err
	}
	for _, r := range v.Results {
		if proof := proofs[r.ID]; proof != nil {
			_, _, err := mapCatalogEnvelope(r.Object[r.ValueKey], proof, func(item map[string]any) (bool, error) {
				path, _ := item["path"].(string)
				if path == "" {
					return false, nil
				}
				parts, e := staticCodeToolPath(path)
				if e != nil {
					return false, e
				}
				if len(parts) == 1 {
					original, known := p.tool(aliasToolKey("", parts[0]), true)
					if known && original != parts[0] {
						return false, nil
					}
					return false, register(path)
				}
				// Client history contains original names; already-aliased histories are idempotent.
				if _, known := p.namespaces[parts[0]]; known {
					if e = register(path); e != nil {
						return false, e
					}
				}
				return false, nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	return p, nil
}
func (p *openCodePlan) catalogProofs(v protocol.AgentRequestView) (map[string]*catalogProjection, error) {
	proofs := map[string]*catalogProjection{}
	for _, c := range v.Calls {
		if c.Name() != "execute" {
			continue
		}
		var analysis *jsAnalysis
		_, err := transformCallCode(c, func(code string) (string, error) {
			var err error
			analysis, err = parseAgentJavaScript(code)
			return code, err
		})
		if err != nil {
			return nil, err
		}
		if c.ID != "" {
			if projection := analysis.catalogProjection(); projection != nil {
				if _, exists := proofs[c.ID]; exists {
					return nil, fmt.Errorf("duplicate catalog call ID")
				}
				proofs[c.ID] = projection
			}
		}
	}
	return proofs, nil
}
func (p *openCodePlan) RewriteRequest(v protocol.AgentRequestView) (bool, error) {
	proofs, err := p.catalogProofs(v)
	if err != nil {
		return false, err
	}
	changed := false
	for _, c := range v.Calls {
		if c.Name() != "execute" {
			continue
		}
		did, err := transformCallCode(c, func(code string) (string, error) {
			a, e := parseAgentJavaScript(code)
			if e != nil {
				return "", e
			}
			return a.rewrite(p, false)
		})
		if err != nil {
			return false, err
		}
		changed = changed || did
	}
	for _, r := range v.Results {
		if proof := proofs[r.ID]; proof != nil {
			next, did, err := mapCatalogEnvelope(r.Object[r.ValueKey], proof, func(item map[string]any) (bool, error) {
				path, _ := item["path"].(string)
				if path == "" {
					return false, nil
				}
				a, e := parseAgentJavaScript("return " + path + ";")
				if e != nil {
					return false, e
				}
				next, e := a.rewrite(p, false)
				if e != nil {
					return false, e
				}
				next = strings.TrimSuffix(strings.TrimPrefix(next, "return "), ";")
				if next != path {
					item["path"] = next
					return true, nil
				}
				return false, nil
			})
			if err != nil {
				return false, err
			}
			if did {
				r.Object[r.ValueKey] = next
				changed = true
			}
		}
	}
	return changed, nil
}
func (p *openCodePlan) RestoreCall(c protocol.AgentToolCall) error {
	if c.Name() != "execute" {
		return nil
	}
	_, err := transformCallCode(c, func(code string) (string, error) {
		a, e := parseAgentJavaScript(code)
		if e != nil {
			return "", e
		}
		return a.rewrite(p, true)
	})
	return err
}

// Only proven return projections are inspected; wrappers here are protocol/tool
// encoding envelopes, not arbitrary recursion through result data.
func mapCatalogEnvelope(value any, projection *catalogProjection, visit func(map[string]any) (bool, error)) (any, bool, error) {
	var apply func(any, *catalogProjection) (bool, error)
	apply = func(v any, p *catalogProjection) (bool, error) {
		if p == nil {
			return false, nil
		}
		changed := false
		if p.Leaf {
			obj, ok := v.(map[string]any)
			if !ok {
				return false, nil
			}
			items, ok := obj["items"].([]any)
			if !ok {
				return false, nil
			}
			if _, ok := obj["remaining"]; !ok {
				return false, nil
			}
			if _, ok := obj["next"]; !ok {
				return false, nil
			}
			for _, v := range items {
				item, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if _, ok := item["path"].(string); !ok {
					continue
				}
				did, e := visit(item)
				if e != nil {
					return false, e
				}
				changed = changed || did
			}
			return changed, nil
		}
		if obj, ok := v.(map[string]any); ok {
			for key, child := range p.Fields {
				did, e := apply(obj[key], child)
				if e != nil {
					return false, e
				}
				changed = changed || did
			}
		}
		if arr, ok := v.([]any); ok {
			for i, child := range p.Elements {
				if i < len(arr) {
					did, e := apply(arr[i], child)
					if e != nil {
						return false, e
					}
					changed = changed || did
				}
			}
		}
		return changed, nil
	}
	var envelope func(any, int) (any, bool, error)
	envelope = func(v any, depth int) (any, bool, error) {
		if depth > 5 {
			return v, false, nil
		}
		if raw, ok := v.(string); ok {
			decoded, err := protocol.DecodeJSON([]byte(raw))
			if err != nil {
				return v, false, nil
			}
			next, did, err := envelope(decoded, depth+1)
			if err != nil || !did {
				return v, did, err
			}
			b, _ := json.Marshal(next)
			return string(b), true, nil
		}
		did, err := apply(v, projection)
		if err != nil || did {
			return v, did, err
		}
		if obj, ok := v.(map[string]any); ok {
			for _, key := range []string{"output", "result", "content"} {
				if x, exists := obj[key]; exists {
					next, did, err := envelope(x, depth+1)
					if err != nil {
						return v, false, err
					}
					if did {
						obj[key] = next
						return v, true, nil
					}
				}
			}
		}
		if arr, ok := v.([]any); ok && len(arr) == 1 {
			if block, ok := arr[0].(map[string]any); ok && block["type"] == "text" {
				next, did, err := envelope(block["text"], depth+1)
				if did {
					block["text"] = next
				}
				return v, did, err
			}
		}
		return v, false, nil
	}
	return envelope(value, 0)
}
