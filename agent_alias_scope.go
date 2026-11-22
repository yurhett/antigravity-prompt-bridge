package main

import (
	"encoding/json"
	"fmt"
	"sort"
)

type toolNameSlot struct {
	object         map[string]any
	key, namespace string
	list           []any
	index          int
	namespaceOnly  bool
}

func (s toolNameSlot) get() string {
	if s.list != nil {
		v, _ := s.list[s.index].(string)
		return v
	}
	v, _ := s.object[s.key].(string)
	return v
}
func (s toolNameSlot) set(v string) {
	if s.list != nil {
		s.list[s.index] = v
	} else {
		s.object[s.key] = v
	}
}
func aliasAgentRequest(body []byte) ([]byte, *agentAliasState, error) {
	result, err := rewriteAgentRequest(body, defaultFilterConfig())
	return result.body, result.state, err
}

type toolDeclaration struct {
	object        map[string]any
	namespace     string
	namespaceOnly bool
}

// Exact protocol paths only. A nested map that happens to contain "name",
// "system", or "tools" inside task data is never inspected.
func collectAgentTools(root any) ([]toolNameSlot, []toolDeclaration) {
	var slots []toolNameSlot
	var definitions []toolDeclaration
	add := func(o map[string]any, key, ns string) {
		if _, ok := o[key].(string); ok {
			slots = append(slots, toolNameSlot{object: o, key: key, namespace: ns})
		}
	}
	var declarations func(any, string)
	declarations = func(value any, namespace string) {
		tools, _ := value.([]any)
		for _, v := range tools {
			tool, _ := v.(map[string]any)
			if tool == nil {
				continue
			}
			if tool["type"] == "namespace" {
				ns, _ := tool["name"].(string)
				slots = append(slots, toolNameSlot{object: tool, key: "name", namespaceOnly: true})
				if ns != "" {
					definitions = append(definitions, toolDeclaration{tool, "", true})
				}
				declarations(tool["tools"], ns)
				continue
			}
			kind, _ := tool["type"].(string)
			if kind == "" || kind == "function" || kind == "custom" {
				add(tool, "name", namespace)
				if name, _ := tool["name"].(string); name != "" {
					definitions = append(definitions, toolDeclaration{tool, namespace, false})
				}
			}
			if function, ok := tool["function"].(map[string]any); ok {
				add(function, "name", namespace)
				if name, _ := function["name"].(string); name != "" && (kind == "" || kind == "function") {
					definitions = append(definitions, toolDeclaration{function, namespace, false})
				}
			}
			for _, key := range []string{"functionDeclarations", "function_declarations"} {
				declarations(tool[key], namespace)
			}
		}
	}
	addFunction := func(v any) {
		if f, ok := v.(map[string]any); ok {
			ns, _ := f["namespace"].(string)
			add(f, "name", ns)
			if ns != "" {
				slots = append(slots, toolNameSlot{object: f, key: "namespace", namespaceOnly: true})
			}
		}
	}
	var choice func(any)
	choice = func(v any) {
		o, ok := v.(map[string]any)
		if !ok {
			return
		}
		// Namespace selectors identify a container, not an executable tool.
		// Unknown selector types likewise have no whitelisted name field.
		kind, _ := o["type"].(string)
		if kind == "namespace" {
			slots = append(slots, toolNameSlot{object: o, key: "name", namespaceOnly: true})
		}
		if kind == "" || kind == "function" || kind == "custom" || kind == "tool" {
			addFunction(o)
			addFunction(o["function"])
		}
		if kind == "allowed_tools" {
			if items, ok := o["tools"].([]any); ok {
				for _, item := range items {
					choice(item)
				}
			}
		}
	}
	for object, _ := root.(map[string]any); object != nil; {
		declarations(object["tools"], "")
		declarations(object["functions"], "")
		choice(object["tool_choice"])
		choice(object["function_call"])
		for _, key := range []string{"toolConfig", "tool_config"} {
			config, _ := object[key].(map[string]any)
			for _, field := range []string{"functionCallingConfig", "function_calling_config"} {
				f, _ := config[field].(map[string]any)
				for _, nameKey := range []string{"allowedFunctionNames", "allowed_function_names"} {
					if names, ok := f[nameKey].([]any); ok {
						for i, v := range names {
							if _, ok := v.(string); ok {
								slots = append(slots, toolNameSlot{list: names, index: i})
							}
						}
					}
				}
			}
		}
		for _, key := range []string{"messages", "input", "contents"} {
			messages, _ := object[key].([]any)
			for _, v := range messages {
				m, _ := v.(map[string]any)
				if m == nil {
					continue
				}
				role, _ := m["role"].(string)
				kind, _ := m["type"].(string)
				if key == "input" && (kind == "function_call" || kind == "custom_tool_call") {
					addFunction(m)
				}
				if role == "tool" || role == "function" {
					add(m, "name", "")
				}
				if role == "assistant" {
					addFunction(m["function_call"])
					calls, _ := m["tool_calls"].([]any)
					for _, v := range calls {
						call, _ := v.(map[string]any)
						addFunction(call["function"])
					}
					blocks, _ := m["content"].([]any)
					for _, v := range blocks {
						block, _ := v.(map[string]any)
						if block["type"] == "tool_use" {
							addFunction(block)
						}
					}
				}
				if key == "contents" && (role == "user" || role == "model") {
					parts, _ := m["parts"].([]any)
					for _, v := range parts {
						part, _ := v.(map[string]any)
						if role == "model" {
							addFunction(part["functionCall"])
							addFunction(part["function_call"])
						} else {
							addFunction(part["functionResponse"])
							addFunction(part["function_response"])
						}
					}
				}
			}
		}
		object, _ = object["request"].(map[string]any)
	}
	return slots, definitions
}

// promptTexts returns the system/developer instructions used for identification.
func promptTexts(root any) []string {
	var texts []string
	transformEligiblePromptFields(root, func(text string) (string, bool) { texts = append(texts, text); return text, false })
	return texts
}

type agentRequestRewrite struct {
	body     []byte
	state    *agentAliasState
	families []string
	stats    agentPromptStats
}

func rewriteAgentRequest(body []byte, cfg filterConfig) (agentRequestRewrite, error) {
	var result agentRequestRewrite
	root, err := decodeAliasJSON(body)
	if err != nil {
		return result, nil
	}
	slots, declarations := collectAgentTools(root)
	view := agentRequestViewWithTools(root, declarations)
	profile := identifyAgent(view)
	if profile == nil {
		return result, nil
	}
	for family := range profile.families {
		result.families = append(result.families, family)
	}
	sort.Strings(result.families)
	plans, err := buildAgentAdapterPlans(view, profile)
	if err != nil {
		return result, err
	}
	functions, namespaces := make(map[string]string), make(map[string]string)
	requiresRestore := len(plans) > 0
	changed := false
	names := make(map[string]string)
	aliasName := func(name string) string {
		alias, ok := names[name]
		if !ok && name != "" {
			alias = profile.aliasName(name)
			names[name] = alias
		}
		return alias
	}
	for _, slot := range slots {
		ns := slot.namespace
		if slot.namespaceOnly {
			ns = slot.get()
		}
		if ns == "" {
			continue
		}
		alias := aliasName(ns)
		if old, ok := namespaces[alias]; ok && old != ns {
			return result, fmt.Errorf("native namespace alias %q is ambiguous", alias)
		}
		namespaces[alias] = ns
		if alias != ns {
			requiresRestore = true
		}
	}
	for _, slot := range slots {
		if slot.namespaceOnly {
			continue
		}
		name := slot.get()
		alias := aliasName(name)
		ns := aliasName(slot.namespace)
		key := aliasToolKey(ns, alias)
		if old, ok := functions[key]; ok && old != name {
			return result, fmt.Errorf("tool name alias %q is ambiguous", alias)
		}
		functions[key] = name
		if alias != name {
			requiresRestore = true
		}
	}
	aliases := declaredToolReferences(declarations, aliasName)
	for _, plan := range plans {
		for _, pair := range plan.SystemIdentifiers() {
			if pair[0] != pair[1] {
				aliases[pair[0]] = pair[1]
			}
		}
	}
	for _, slot := range slots {
		alias := aliasName(slot.get())
		if alias != slot.get() {
			slot.set(alias)
			changed = true
		}
	}
	for _, plan := range plans {
		did, err := plan.RewriteRequest(view)
		if err != nil {
			return result, adapterPlanError(err)
		}
		changed = changed || did
	}
	rewriter := agentPromptRewriter{
		profile: profile, aliases: aliases, custom: normalizeMappings(cfg.CustomMappings),
	}
	_, systemChanged := transformEligiblePromptFields(root, func(text string) (string, bool) {
		return rewriter.rewrite(text, true)
	})
	changed = changed || systemChanged
	for _, declaration := range declarations {
		changed = rewriteToolDocumentation(declaration.object, func(text string) (string, bool) {
			return rewriter.rewrite(text, false)
		}) || changed
	}
	result.stats = rewriter.stats
	if !changed {
		return result, nil
	}
	if requiresRestore {
		result.state = &agentAliasState{
			functions: functions, namespaces: namespaces, adapters: plans, renamed: true,
			calls: make(map[string]*streamAliasCall),
		}
	}
	result.body, err = json.Marshal(root)
	return result, err
}
