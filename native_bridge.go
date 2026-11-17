package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Verbatim identity block observed in official agy gen_metadata. CLI-specific
// tools, filesystem paths and permission rules are deliberately not imported.
const nativeAntigravitySystem = "<identity>\n" + nativeAntigravityIdentity + "\nYou are working with a USER to solve their task.\nThe USER will send you requests, which you must always prioritize addressing. User requests are enclosed within <USER_REQUEST> tags.\n</identity>"

type bridgeInstruction struct {
	role string
	text string
}

func rewriteProviderRequestBody(body []byte, cfg filterConfig, provider string) ([]byte, bool) {
	if !providerFilterEnabled(provider, cfg) {
		return nil, false
	}
	next, changed := rewriteRequestBodyWithConfig(body, cfg)
	if !changed {
		next = body
	}
	if cfg.AntigravityBridgeSystemPrompt && strings.EqualFold(strings.TrimSpace(provider), "antigravity") {
		if bridged, moved := bridgeNativeSystemPrompt(next); moved {
			return bridged, true
		}
	}
	if changed {
		return next, true
	}
	return nil, false
}

func bridgeNativeSystemPrompt(body []byte) ([]byte, bool) {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil || root == nil || !bridgePromptObject(root) {
		return nil, false
	}
	next, err := json.Marshal(root)
	return next, err == nil
}

func bridgePromptObject(root map[string]any) bool {
	// Only re-enter the known Antigravity envelope, never tool/metadata JSON.
	if request, ok := root["request"].(map[string]any); ok {
		return bridgePromptObject(request)
	}
	if _, ok := root["contents"]; ok {
		return bridgeGeminiPrompt(root)
	}
	if _, ok := root["input"]; ok {
		return bridgeResponsesPrompt(root)
	}
	if _, ok := root["messages"]; ok {
		return bridgeChatPrompt(root)
	}
	return false
}

// Refuse to relocate mixed/non-text system content rather than dropping it.
// Existing rewriting still applies if relocation cannot safely be performed.
func bridgePromptText(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, true
	case []any:
		texts := make([]string, 0, len(value))
		for _, block := range value {
			text, ok := bridgePromptText(block)
			if !ok {
				return "", false
			}
			texts = append(texts, text)
		}
		return strings.Join(texts, "\n\n"), true
	case map[string]any:
		text, ok := value["text"].(string)
		if !ok {
			return "", false
		}
		if kind, exists := value["type"]; exists && kind != "text" && kind != "input_text" {
			return "", false
		}
		for key := range value {
			if key != "text" && key != "type" && key != "cache_control" {
				return "", false
			}
		}
		return text, true
	default:
		return "", false
	}
}

func addBridgeInstruction(entries *[]bridgeInstruction, role, text string) {
	if strings.TrimSpace(text) != "" && text != nativeAntigravitySystem {
		*entries = append(*entries, bridgeInstruction{role: role, text: text})
	}
}

func bridgeUserText(entries []bridgeInstruction) string {
	var b strings.Builder
	b.WriteString("<USER_REQUEST>\nFollow the system and developer instructions in the conversation below, then answer the latest user request.\n\n")
	for i, entry := range entries {
		fmt.Fprintf(&b, "--- message %d role=%s ---\n", i+1, entry.role)
		b.WriteString(entry.text)
		b.WriteByte('\n')
	}
	b.WriteString("</USER_REQUEST>")
	return b.String()
}

func splitBridgeMessages(messages []any) ([]any, []bridgeInstruction, bool) {
	kept := make([]any, 0, len(messages))
	var entries []bridgeInstruction
	for _, value := range messages {
		message, ok := value.(map[string]any)
		if !ok {
			kept = append(kept, value)
			continue
		}
		role, _ := message["role"].(string)
		role = strings.ToLower(strings.TrimSpace(role))
		if role != "system" && role != "developer" {
			kept = append(kept, value)
			continue
		}
		text, ok := bridgePromptText(message["content"])
		if !ok {
			return nil, nil, false
		}
		addBridgeInstruction(&entries, role, text)
	}
	return kept, entries, true
}

func bridgeChatPrompt(root map[string]any) bool {
	messages, ok := root["messages"].([]any)
	if !ok {
		return false
	}
	kept, entries, ok := splitBridgeMessages(messages)
	if !ok {
		return false
	}
	_, anthropic := root["system"]
	if anthropic {
		text, valid := bridgePromptText(root["system"])
		if !valid {
			return false
		}
		var system []bridgeInstruction
		addBridgeInstruction(&system, "system", text)
		entries = append(system, entries...)
	}
	if len(entries) == 0 {
		return false
	}
	user := map[string]any{"role": "user", "content": bridgeUserText(entries)}
	if anthropic {
		root["system"] = nativeAntigravitySystem
		root["messages"] = append([]any{user}, kept...)
	} else {
		system := map[string]any{"role": "system", "content": nativeAntigravitySystem}
		root["messages"] = append([]any{system, user}, kept...)
	}
	return true
}

func bridgeResponsesPrompt(root map[string]any) bool {
	var input []any
	switch value := root["input"].(type) {
	case string:
		input = []any{map[string]any{"role": "user", "content": value}}
	case []any:
		input = value
	default:
		return false
	}
	kept, entries, ok := splitBridgeMessages(input)
	if !ok {
		return false
	}
	if value, exists := root["instructions"]; exists && value != nil {
		text, valid := bridgePromptText(value)
		if !valid {
			return false
		}
		var instructions []bridgeInstruction
		addBridgeInstruction(&instructions, "system", text)
		entries = append(instructions, entries...)
	}
	if len(entries) == 0 {
		return false
	}
	root["instructions"] = nativeAntigravitySystem
	user := map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": bridgeUserText(entries)}}}
	root["input"] = append([]any{user}, kept...)
	return true
}

func bridgeGeminiPrompt(root map[string]any) bool {
	contents, ok := root["contents"].([]any)
	if !ok {
		return false
	}
	key := "systemInstruction"
	if _, exists := root[key]; !exists {
		key = "system_instruction"
	} else if _, duplicate := root["system_instruction"]; duplicate {
		return false
	}
	system, ok := root[key].(map[string]any)
	if !ok {
		return false
	}
	parts, ok := system["parts"].([]any)
	if !ok {
		return false
	}
	text, ok := bridgePromptText(parts)
	if !ok {
		return false
	}
	var entries []bridgeInstruction
	addBridgeInstruction(&entries, "system", text)
	if len(entries) == 0 {
		return false
	}
	// Keep system-level extension fields, but replace only its prompt parts.
	system["parts"] = []any{map[string]any{"text": nativeAntigravitySystem}}
	user := map[string]any{"role": "user", "parts": []any{map[string]any{"text": bridgeUserText(entries)}}}
	root["contents"] = append([]any{user}, contents...)
	return true
}
