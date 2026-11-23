package protocol

type ToolNameSlot struct {
	object        map[string]any
	key           string
	Namespace     string
	list          []any
	index         int
	NamespaceOnly bool
}

func (s ToolNameSlot) Name() string {
	if s.list != nil {
		v, _ := s.list[s.index].(string)
		return v
	}
	v, _ := s.object[s.key].(string)
	return v
}
func (s ToolNameSlot) SetName(v string) {
	if s.list != nil {
		s.list[s.index] = v
	} else {
		s.object[s.key] = v
	}
}

type ToolDeclaration struct {
	Object        map[string]any
	Namespace     string
	NamespaceOnly bool
}

// Exact protocol paths only. A nested map that happens to contain "name",
// "system", or "tools" inside task data is never inspected.
func CollectTools(root any) ([]ToolNameSlot, []ToolDeclaration) {
	var slots []ToolNameSlot
	var definitions []ToolDeclaration
	add := func(o map[string]any, key, ns string) {
		if _, ok := o[key].(string); ok {
			slots = append(slots, ToolNameSlot{object: o, key: key, Namespace: ns})
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
				slots = append(slots, ToolNameSlot{object: tool, key: "name", NamespaceOnly: true})
				if ns != "" {
					definitions = append(definitions, ToolDeclaration{tool, "", true})
				}
				declarations(tool["tools"], ns)
				continue
			}
			kind, _ := tool["type"].(string)
			if kind == "" || kind == "function" || kind == "custom" {
				add(tool, "name", namespace)
				if name, _ := tool["name"].(string); name != "" {
					definitions = append(definitions, ToolDeclaration{tool, namespace, false})
				}
			}
			if function, ok := tool["function"].(map[string]any); ok {
				add(function, "name", namespace)
				if name, _ := function["name"].(string); name != "" && (kind == "" || kind == "function") {
					definitions = append(definitions, ToolDeclaration{function, namespace, false})
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
				slots = append(slots, ToolNameSlot{object: f, key: "namespace", NamespaceOnly: true})
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
			slots = append(slots, ToolNameSlot{object: o, key: "name", NamespaceOnly: true})
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
								slots = append(slots, ToolNameSlot{list: names, index: i})
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

var toolSchemaKeys = []string{"parameters", "input_schema", "parametersJsonSchema", "parameters_json_schema"}

func TransformToolDocumentation(declaration map[string]any, rewrite func(string) (string, bool)) bool {
	changed := rewriteDescription(declaration, rewrite)
	for _, key := range toolSchemaKeys {
		changed = rewriteSchemaDescriptions(declaration[key], rewrite) || changed
	}
	return changed
}

func rewriteDescription(object map[string]any, rewrite func(string) (string, bool)) bool {
	if text, ok := object["description"].(string); ok {
		if next, changed := rewrite(text); changed {
			object["description"] = next
			return true
		}
	}
	return false
}

func rewriteSchemaDescriptions(value any, rewrite func(string) (string, bool)) bool {
	schema, ok := value.(map[string]any)
	if !ok {
		return false
	}
	changed := rewriteDescription(schema, rewrite)
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependencies", "dependentSchemas"} {
		children, _ := schema[key].(map[string]any)
		for _, child := range children {
			changed = rewriteSchemaDescriptions(child, rewrite) || changed
		}
	}
	for _, key := range []string{"items", "prefixItems", "anyOf", "any_of", "allOf", "oneOf", "additionalItems", "additionalProperties", "contains", "propertyNames", "unevaluatedItems", "unevaluatedProperties", "not", "if", "then", "else"} {
		switch child := schema[key].(type) {
		case []any:
			for _, item := range child {
				changed = rewriteSchemaDescriptions(item, rewrite) || changed
			}
		case map[string]any:
			changed = rewriteSchemaDescriptions(child, rewrite) || changed
		}
	}
	return changed
}
