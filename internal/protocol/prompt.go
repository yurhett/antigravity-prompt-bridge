package protocol

import (
	"strings"
)

// TransformPromptFields visits only protocol-defined system/developer
// prompt text. It intentionally does not recurse through arbitrary JSON: user
// messages, tool payloads, identifiers, and metadata must remain untouched.
func TransformPromptFields(root any, transform func(string) (string, bool)) (any, bool) {
	return transformPromptObject(root, true, transform)
}

func transformPromptObject(value any, allowPromptFields bool, transform func(string) (string, bool)) (any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return value, false
	}
	changed := false
	if allowPromptFields {
		for _, key := range []string{"system", "instructions"} {
			child, exists := object[key]
			if !exists {
				continue
			}
			next, childChanged := transformPromptTextContainer(child, transform)
			if childChanged {
				object[key] = next
				changed = true
			}
		}
		for _, key := range []string{"input", "messages"} {
			child, exists := object[key]
			if !exists {
				continue
			}
			next, childChanged := transformRoleMessages(child, transform)
			if childChanged {
				object[key] = next
				changed = true
			}
		}
		for _, key := range []string{"systemInstruction", "system_instruction"} {
			child, exists := object[key]
			if !exists {
				continue
			}
			next, childChanged := transformGeminiSystemInstruction(child, transform)
			if childChanged {
				object[key] = next
				changed = true
			}
		}
	}

	// CPA may wrap the original request in an Antigravity `request` object.
	// Re-enter only that protocol wrapper instead of scanning every nested map.
	if child, exists := object["request"]; exists {
		next, childChanged := transformPromptObject(child, true, transform)
		if childChanged {
			object["request"] = next
			changed = true
		}
	}
	return object, changed
}

func transformPromptTextContainer(value any, transform func(string) (string, bool)) (any, bool) {
	switch typed := value.(type) {
	case string:
		return transform(typed)
	case []any:
		changed := false
		for index, child := range typed {
			next, childChanged := transformPromptTextContainer(child, transform)
			if childChanged {
				typed[index] = next
				changed = true
			}
		}
		return typed, changed
	case map[string]any:
		// Anthropic and Responses text blocks carry their prompt in `text`.
		if text, ok := typed["text"].(string); ok {
			next, childChanged := transform(text)
			if childChanged {
				typed["text"] = next
			}
			return typed, childChanged
		}
	}
	return value, false
}

func transformRoleMessages(value any, transform func(string) (string, bool)) (any, bool) {
	switch typed := value.(type) {
	case []any:
		changed := false
		for index, child := range typed {
			next, childChanged := transformRoleMessage(child, transform)
			if childChanged {
				typed[index] = next
				changed = true
			}
		}
		return typed, changed
	case map[string]any:
		return transformRoleMessage(typed, transform)
	default:
		return value, false
	}
}

func transformRoleMessage(value any, transform func(string) (string, bool)) (any, bool) {
	message, ok := value.(map[string]any)
	if !ok {
		return value, false
	}
	role, _ := message["role"].(string)
	role = strings.ToLower(strings.TrimSpace(role))
	if role != "system" && role != "developer" {
		return value, false
	}
	content, exists := message["content"]
	if !exists {
		return value, false
	}
	next, changed := transformMessageContent(content, transform)
	if changed {
		message["content"] = next
	}
	return message, changed
}

func transformMessageContent(value any, transform func(string) (string, bool)) (any, bool) {
	switch typed := value.(type) {
	case string:
		return transform(typed)
	case []any:
		changed := false
		for index, child := range typed {
			block, ok := child.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			blockType = strings.ToLower(strings.TrimSpace(blockType))
			if blockType != "text" && blockType != "input_text" {
				continue
			}
			text, ok := block["text"].(string)
			if !ok {
				continue
			}
			next, childChanged := transform(text)
			if childChanged {
				block["text"] = next
				typed[index] = block
				changed = true
			}
		}
		return typed, changed
	case map[string]any:
		blockType, _ := typed["type"].(string)
		blockType = strings.ToLower(strings.TrimSpace(blockType))
		if blockType != "text" && blockType != "input_text" {
			return value, false
		}
		text, ok := typed["text"].(string)
		if !ok {
			return value, false
		}
		next, changed := transform(text)
		if changed {
			typed["text"] = next
		}
		return typed, changed
	default:
		return value, false
	}
}

func transformGeminiSystemInstruction(value any, transform func(string) (string, bool)) (any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return value, false
	}
	parts, ok := object["parts"].([]any)
	if !ok {
		return value, false
	}
	changed := false
	for index, part := range parts {
		partObject, ok := part.(map[string]any)
		if !ok {
			continue
		}
		text, ok := partObject["text"].(string)
		if !ok {
			continue
		}
		next, partChanged := transform(text)
		if partChanged {
			partObject["text"] = next
			parts[index] = partObject
			changed = true
		}
	}
	return object, changed
}

// PromptTexts returns the system/developer instructions used for identification.
func PromptTexts(root any) []string {
	var texts []string
	TransformPromptFields(root, func(text string) (string, bool) { texts = append(texts, text); return text, false })
	return texts
}
