package protocol

import "strings"

// EnsureSystemIdentity makes the request introduce itself with identity.
// The sentence is prepended to the first system/developer text unless some
// instruction already contains it. A request without instructions gets one
// where its fields or the source format identify how to add one.
func EnsureSystemIdentity(root any, identity, sourceFormat string) bool {
	texts := PromptTexts(root)
	for _, text := range texts {
		if strings.HasPrefix(strings.TrimSpace(text), identity) {
			return false
		}
	}
	if len(texts) > 0 {
		done := false
		_, changed := TransformPromptFields(root, func(text string) (string, bool) {
			if done {
				return text, false
			}
			done = true
			if strings.TrimSpace(text) == "" {
				return identity, true
			}
			return identity + "\n\n" + text, true
		})
		return changed
	}
	return insertSystemIdentity(root, identity, sourceFormat)
}

func insertSystemIdentity(value any, identity, sourceFormat string) bool {
	root, ok := value.(map[string]any)
	if !ok {
		return false
	}
	// CPA may wrap the request in an Antigravity `request` envelope.
	if request, ok := root["request"].(map[string]any); ok {
		return insertSystemIdentity(request, identity, sourceFormat)
	}
	if _, ok := root["contents"]; ok {
		key := "systemInstruction"
		if _, exists := root["system_instruction"]; exists {
			if _, camel := root[key]; !camel {
				key = "system_instruction"
			}
		}
		instruction, _ := root[key].(map[string]any)
		if instruction == nil {
			instruction = map[string]any{}
		}
		parts, _ := instruction["parts"].([]any)
		instruction["parts"] = append([]any{map[string]any{"text": identity}}, parts...)
		root[key] = instruction
		return true
	}
	if _, ok := root["input"]; ok {
		if current, exists := root["instructions"]; exists && current != nil {
			return false
		}
		root["instructions"] = identity
		return true
	}
	messages, ok := root["messages"].([]any)
	if !ok {
		return false
	}
	switch sourceFormat {
	case "openai":
		root["messages"] = append([]any{map[string]any{"role": "system", "content": identity}}, messages...)
		return true
	case "claude", "anthropic":
		if blocks, ok := root["system"].([]any); ok {
			root["system"] = append([]any{map[string]any{"type": "text", "text": identity}}, blocks...)
		} else {
			root["system"] = identity
		}
		return true
	}
	return false
}
