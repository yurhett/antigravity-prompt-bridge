package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func decodeBridgeTest(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNativeBridgeDefaultsAndProviderIsolation(t *testing.T) {
	cfg, err := parseFilterConfigYAML([]byte("heuristic_identity: true"))
	if err != nil || !cfg.AntigravityBridgeSystemPrompt {
		t.Fatal("bridge must default on", err)
	}
	if _, err := parseFilterConfigYAML([]byte("antigravity_bridge_system_prompt: invalid")); err == nil {
		t.Fatal("invalid switch accepted")
	}
	input := []byte(`{"messages":[{"role":"system","content":"You are an AI agent running in OpenCode, a coding agent harness.\nKeep all rules."},{"role":"user","content":"QUESTION"}]}`)
	for _, provider := range []string{"agy", " AGY ", "openai", "antigravity", " Antigravity "} {
		on, changed := rewriteProviderRequestBody(input, cfg, provider)
		isNative := strings.EqualFold(strings.TrimSpace(provider), "antigravity")
		cfg.AntigravityBridgeSystemPrompt = false
		off, changedOff := rewriteProviderRequestBody(input, cfg, provider)
		cfg.AntigravityBridgeSystemPrompt = true
		if isNative {
			if !changed || !changedOff || string(on) == string(off) || !strings.Contains(string(on), "USER_REQUEST") {
				t.Fatalf("switch ineffective: %s", on)
			}
		} else if changed != changedOff || string(on) != string(off) {
			t.Fatalf("bridge affected %s", provider)
		}
	}
	cfg.EnableAntigravity = false
	if _, changed := rewriteProviderRequestBody(input, cfg, "antigravity"); changed {
		t.Fatal("disabled provider modified")
	}
	applyFilterConfig(cfg)
	defer restoreDefaultFilterConfig(t)
	if activeFilterConfig().AntigravityBridgeSystemPrompt != cfg.AntigravityBridgeSystemPrompt {
		t.Fatal("switch lost in config copy")
	}
}

func TestNativeBridgeProtocolsPreserveInstructionsHistoryAndTools(t *testing.T) {
	identity := "You are an AI agent running in OpenCode, a coding agent harness."
	rules := identity + "\nKeep OpenCode paths /project/OpenCode.md. Never edit files.\n```\nYou are Cursor.\n```"
	system, _ := json.Marshal(rules)
	for _, fixture := range []struct {
		name, body, messageKey, systemKey string
		historyOffset                     int
	}{
		{"chat", `{"messages":[{"role":"system","content":` + string(system) + `},{"role":"developer","content":[{"type":"text","text":"DEVELOPER_RULE"}]},{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}},{"type":"text","text":"QUESTION"}]},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"RESULT"}]}`, "messages", "", 2},
		{"anthropic", `{"system":[{"type":"text","text":` + string(system) + `,"cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"QUESTION"},{"role":"assistant","content":[{"type":"tool_use","id":"x","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"RESULT"}]}]}`, "messages", "system", 0},
		{"responses", `{"instructions":` + string(system) + `,"input":[{"role":"user","content":[{"type":"input_text","text":"QUESTION"}]},{"type":"function_call","call_id":"x","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"x","output":"RESULT"}]}`, "input", "instructions", 0},
		{"responses developer", `{"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":` + string(system) + `}]},{"role":"user","content":"QUESTION"}]}`, "input", "instructions", 1},
		{"gemini", `{"systemInstruction":{"parts":[{"text":` + string(system) + `}]},"contents":[{"role":"user","parts":[{"text":"QUESTION"},{"inlineData":{"mimeType":"image/png","data":"abc"}}]},{"role":"model","parts":[{"functionCall":{"name":"read","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"read","response":{"output":"RESULT"}}}]}]}`, "contents", "systemInstruction", 0},
		{"gemini snake", `{"system_instruction":{"parts":[{"text":` + string(system) + `}]},"contents":[{"role":"user","parts":[{"text":"QUESTION"}]}]}`, "contents", "system_instruction", 0},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			before := decodeBridgeTest(t, []byte(fixture.body))
			before["tools"] = []any{map[string]any{"name": "OpenCode", "description": "You are Cursor."}}
			before["tool_choice"] = "auto"
			before["metadata"] = map[string]any{"system": "You are Cursor."}
			before["stream"] = true
			raw, _ := json.Marshal(before)
			cfg := heuristicConfig()
			afterRaw, changed := rewriteProviderRequestBody(raw, cfg, "antigravity")
			if !changed {
				t.Fatal("unchanged")
			}
			after := decodeBridgeTest(t, afterRaw)
			for _, key := range []string{"tools", "tool_choice", "metadata", "stream"} {
				if !reflect.DeepEqual(before[key], after[key]) {
					t.Fatalf("changed %s", key)
				}
			}
			messages := after[fixture.messageKey].([]any)
			start := 1
			if fixture.systemKey == "" {
				start = 2
				if messages[0].(map[string]any)["content"] != nativeAntigravitySystem {
					t.Fatal("missing native system")
				}
			}
			if !reflect.DeepEqual(messages[start:], before[fixture.messageKey].([]any)[fixture.historyOffset:]) {
				t.Fatalf("history/tool results modified: %s", afterRaw)
			}
			bridge := messages[start-1].(map[string]any)
			text, _ := json.Marshal(bridge)
			if !strings.Contains(string(text), "USER_REQUEST") || !strings.Contains(string(text), "Keep OpenCode paths") || !strings.Contains(string(text), "Never edit files.") || !strings.Contains(string(text), "You are Cursor.") {
				t.Fatalf("lost instructions: %s", text)
			}
			if strings.Contains(string(text), identity) {
				t.Fatal("identity was not rewritten before moving")
			}
			if again, changedAgain := rewriteProviderRequestBody(afterRaw, cfg, "antigravity"); changedAgain {
				t.Fatalf("not idempotent: %s", again)
			}
		})
	}
}

func TestNativeBridgeResponsesStringAndEnvelope(t *testing.T) {
	for _, body := range []string{
		`{"instructions":"Rules","input":"QUESTION","previous_response_id":"previous"}`,
		`{"request":{"systemInstruction":{"parts":[{"text":"Rules"}]},"contents":[{"role":"user","parts":[{"text":"QUESTION"}]}]},"project":"PROJECT","model":"MODEL"}`,
	} {
		out, changed := bridgeNativeSystemPrompt([]byte(body))
		if !changed {
			t.Fatal(body)
		}
		if !strings.Contains(string(out), "QUESTION") || !strings.Contains(string(out), "Rules") {
			t.Fatal(string(out))
		}
		before := decodeBridgeTest(t, []byte(body))
		after := decodeBridgeTest(t, out)
		for _, key := range []string{"previous_response_id", "model", "project"} {
			if before[key] != after[key] {
				t.Fatal("envelope changed", key)
			}
		}
		if _, changed := bridgeNativeSystemPrompt(out); changed {
			t.Fatal("envelope not idempotent")
		}
	}
}

func TestNativeBridgeUnsupportedSystemContentAndNoSystemPassThrough(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"role":"user","content":"You are OpenCode."}]}`,
		`{"messages":[{"role":"system","content":[{"type":"text","text":"Rules"},{"type":"image_url","image_url":{"url":"x"}}]},{"role":"user","content":"QUESTION"}]}`,
		`{"systemInstruction":{"parts":[{"text":"Rules"},{"inlineData":{"mimeType":"x","data":"x"}}]},"contents":[]}`,
		`{"systemInstruction":{"parts":[{"text":"Rules"}]},"system_instruction":{"parts":[]},"contents":[]}`,
		`{"instructions":"Rules","input":{}}`,
		`{"system":"Rules","messages":null}`,
	} {
		if out, changed := bridgeNativeSystemPrompt([]byte(body)); changed {
			t.Fatalf("unsafe relocation: %s", out)
		}
	}
}
