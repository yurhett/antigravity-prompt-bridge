package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func identityConfig(t *testing.T) (filterConfig, map[string]string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/verified-identities.json")
	if err != nil {
		t.Fatal(err)
	}
	var maps map[string]string
	if err = json.Unmarshal(raw, &maps); err != nil {
		t.Fatal(err)
	}
	cfg := defaultFilterConfig()
	for key, value := range maps {
		cfg.CustomMappings = append(cfg.CustomMappings, rewriteMapping{Match: key, Replacement: value})
	}
	return cfg, maps
}
func TestCollectedIdentitiesAndAlreadyRenamedVariants(t *testing.T) {
	cfg, maps := identityConfig(t)
	for key, want := range maps {
		for _, input := range []string{key, strings.ToUpper(key)} {
			raw, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "system", "content": input + "\nKeep the project rules."}}})
			out, changed := rewriteRequestBodyWithConfig(raw, cfg)
			if !changed {
				t.Fatalf("not normalized: %s", key)
			}
			var d map[string]any
			json.Unmarshal(out, &d)
			got := d["messages"].([]any)[0].(map[string]any)["content"]
			if got != want+"\nKeep the project rules." {
				t.Fatalf("%s => %v", key, got)
			}
			second, changedAgain := rewriteRequestBodyWithConfig(out, cfg)
			if changedAgain && string(second) != string(out) {
				t.Fatalf("not idempotent: %s", key)
			}
		}
	}
}
func TestIdentityConfigPreservesUserToolsAndModeRules(t *testing.T) {
	cfg, _ := identityConfig(t)
	identity := "You are an AI agent running in OpenCode, a coding agent harness."
	messages := []any{map[string]any{"role": "system", "content": identity + "\nPlan mode: do not edit files. See /tmp/.codex/CODEX.md and https://example.test/Codex.md"}, map[string]any{"role": "developer", "content": identity}, map[string]any{"role": "user", "content": identity}, map[string]any{"role": "assistant", "content": identity}, map[string]any{"role": "tool", "content": identity, "tool_call_id": "call_Codex"}}
	body := map[string]any{"messages": messages, "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "Codex", "description": identity}}}, "metadata": map[string]any{"reference": identity}}
	raw, _ := json.Marshal(body)
	out, _ := rewriteRequestBodyWithConfig(raw, cfg)
	var after map[string]any
	json.Unmarshal(out, &after)
	am := after["messages"].([]any)
	if am[0].(map[string]any)["content"] != "You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team.\nPlan mode: do not edit files. See /tmp/.codex/CODEX.md and https://example.test/Codex.md" {
		t.Fatalf("wrong system: %s", out)
	}
	for i := 2; i < len(messages); i++ {
		if !reflect.DeepEqual(messages[i], am[i]) {
			t.Fatalf("changed role %d", i)
		}
	}
	for _, field := range []string{"tools", "metadata"} {
		if !reflect.DeepEqual(body[field], after[field]) {
			t.Fatalf("changed %s", field)
		}
	}
}
func TestIdentityConfigHandlesEveryPromptProtocol(t *testing.T) {
	cfg, _ := identityConfig(t)
	from := "You are an AI agent running in OpenCode, a coding agent harness."
	want := "You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team."
	fixtures := []string{
		`{"system":"` + from + `"}`,
		`{"instructions":"` + from + `"}`,
		`{"input":[{"role":"developer","content":[{"type":"input_text","text":"` + from + `"}]}]}`,
		`{"systemInstruction":{"parts":[{"text":"` + from + `"}]}}`,
	}
	for _, fixture := range fixtures {
		out, changed := rewriteRequestBodyWithConfig([]byte(fixture), cfg)
		if !changed || !strings.Contains(string(out), want) || strings.Contains(string(out), from) {
			t.Fatalf("bad normalization: %s", out)
		}
	}
}
