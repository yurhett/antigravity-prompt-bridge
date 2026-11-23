package rewrite

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

func TestOfficialAgentToolExcerpts(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/agent-tool-sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Agent, Kind, Excerpt, Expected string
		URL                            string `json:"source_url"`
		SHA                            string `json:"source_sha256"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Agent+"/"+tc.Excerpt[:min(24, len(tc.Excerpt))], func(t *testing.T) {
			if tc.Kind != "official_verbatim_excerpt" || !strings.HasPrefix(tc.URL, "https://") || len(tc.SHA) != 64 {
				t.Fatal("missing source evidence")
			}
			got := tc.Excerpt
			if profile := defaultBrandCatalog.identify([]string{tc.Excerpt}); profile != nil {
				rewriter := agentPromptRewriter{profile: profile}
				got, _ = rewriter.rewrite(tc.Excerpt, true)
			}
			if got != tc.Expected {
				t.Fatalf("official excerpt changed unexpectedly:\n%s\nwant:\n%s", got, tc.Expected)
			}
		})
	}
}

// These protocol fixtures are synthetic. Official excerpts live separately in
// agent-tool-sources.json and are never represented as complete runtime prompts.

func TestAgentBrandsCatalogueAndAmbiguity(t *testing.T) {
	if len(agentBrands) != 61 {
		t.Fatalf("catalogue has %d names", len(agentBrands))
	}
	for _, brand := range agentBrands {
		t.Run(brand.Name, func(t *testing.T) {
			text := "You are " + brand.Name + ", a coding agent."
			profile := defaultBrandCatalog.identify([]string{text})
			if profile == nil || !profile.families[brand.family()] {
				t.Fatal("self-identity not recognized")
			}
			rewriter := agentPromptRewriter{profile: profile}
			if got, _ := rewriter.rewrite(text, true); got != nativeAntigravityIdentity {
				t.Fatalf("%q", got)
			}
		})
	}
	text := "const pi = 3.14; let cursor = db.cursor(); a goose swims; Cascade mountains; aiderX; myOpenCodePlugin; Gemini Claude Qwen."
	if defaultBrandCatalog.identify([]string{text}) != nil {
		t.Fatal("ordinary text identified an agent")
	}
	anchored := defaultBrandCatalog.identify([]string{"You are a coding assistant running inside pi, a coding agent harness. You are Cursor, a coding agent. You are Hermes Agent. You are Goose, an AI agent."})
	rewriter := agentPromptRewriter{profile: anchored}
	if got, _ := rewriter.rewrite(text, true); got != text {
		t.Fatalf("wrong scoped whole-word rewrite: %q", got)
	}
	// The identified agent's own capitalized name is its product name.
	if got, _ := rewriter.rewrite("Hermes can browse.", true); got != "Antigravity can browse." {
		t.Fatal(got)
	}
	m := defaultBrandCatalog.identify([]string{"You are a coding assistant running inside pi, a coding agent harness."})
	rewriter.profile = m
	if got, _ := rewriter.rewrite("Use tools.pi.read and /tmp/.pi/settings.json; const pi = 3.14", true); got != "Use tools.pi.read and /tmp/.pi/settings.json; const pi = 3.14" {
		t.Fatal(got)
	}
	for _, name := range []string{"read", "bash", "execute_command", "terminal", "apply_patch"} {
		if got := m.aliasName(name); got != name {
			t.Fatal(got)
		}
	}
	if got := m.aliasName("pi_extension"); got != "antigravity_extension" {
		t.Fatal(got)
	}
}

func TestAgentAliasUsesOriginalContext(t *testing.T) {
	original := []byte(`{"system":"You are a coding assistant running inside pi, a coding agent harness.\nPi documentation; tools.pi.read.","tools":[{"name":"read","description":"Read via tools.pi.read"}],"messages":[{"role":"user","content":"Pi; const pi = 3.14; OpenCode; tools.pi.read"}]}`)
	cfg := heuristicConfig()
	cfg.AntigravityBridgeSystemPrompt = false
	result, err := rewriteAgentRequest(original, cfg)
	if err != nil || result.State != nil || len(result.Families) != 1 || result.Families[0] != "Pi" {
		t.Fatal(err)
	}
	got := result.Body
	if strings.Contains(string(got), "Pi documentation") || !strings.Contains(string(got), "tools.pi.read") || !strings.Contains(string(got), "Pi; const pi = 3.14; OpenCode; tools.pi.read") {
		t.Fatal(string(got))
	}
	m := agentPromptRewriter{profile: defaultBrandCatalog.identify([]string{"你是 OpenCode。"})}
	if got, _ := m.rewrite("在OpenCode中使用Gemini CLI工具", true); got != "在Antigravity中使用Gemini CLI工具" {
		t.Fatal("Chinese boundary")
	}
}

func TestAgentEncodedArgumentsPreserveOpaqueValues(t *testing.T) {
	before := `{"system":"You are Codex.","tools":[{"name":"codex_probe"}],"messages":[{"role":"assistant","tool_calls":[{"function":{"name":"codex_probe","arguments":"{\"id\":\"codex-id\",\"thoughtSignature\":\"codex-secret\",\"path\":\"/tmp/codex\"}"}}]}]}`
	got, _, err := aliasAgentRequest([]byte(before))
	if err != nil {
		t.Fatal(err)
	}
	value, _ := protocol.DecodeJSON(got)
	f := value.(map[string]any)["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
	args, _ := protocol.DecodeJSON([]byte(f["arguments"].(string)))
	a := args.(map[string]any)
	if a["id"] != "codex-id" || a["thoughtSignature"] != "codex-secret" || a["path"] != "/tmp/codex" {
		t.Fatal(a)
	}
}

func aliasProtocolState(t *testing.T) *AliasState {
	t.Helper()
	_, s, err := aliasAgentRequest([]byte(`{"system":"You are Codex. Use tools.codex.read. Files: /tmp/codex.","tools":[{"type":"function","function":{"name":"codex_probe","parameters":{"type":"object","properties":{"codex_key":{"type":"string"},"code":{"type":"string"}}}}},{"type":"custom","name":"codex_patch","format":{"type":"text"}}]}`))
	if err != nil || s == nil {
		t.Fatal(err)
	}
	return s
}

func runAliasStream(t *testing.T, s *AliasState, events []string, fragment int) []map[string]any {
	t.Helper()
	wire := ""
	for _, event := range events {
		wire += "data: " + event + "\r\n\r\n"
	}
	var output strings.Builder
	for start := 0; start < len(wire); start += fragment {
		end := start + fragment
		if end > len(wire) {
			end = len(wire)
		}
		body, drop, err := s.RestoreChunk([]byte(wire[start:end]))
		if err != nil {
			t.Fatal(err)
		}
		if !drop {
			output.Write(body)
		}
	}
	var result []map[string]any
	for _, frame := range strings.Split(output.String(), "\n\n") {
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "data: ") {
				raw := strings.TrimPrefix(line, "data: ")
				if raw == "[DONE]" {
					continue
				}
				var event map[string]any
				if err := json.Unmarshal([]byte(raw), &event); err != nil {
					t.Fatal(err)
				}
				result = append(result, event)
			}
		}
	}
	return result
}

func TestAgentChatStreamingParallelAndFragmentedNames(t *testing.T) {
	events := []string{
		`{"id":"keep","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-codex-opaque","type":"function","function":{"name":"anti","arguments":"{\"antigravity_key\":\""}},{"index":1,"id":"other","type":"function","function":{"name":"antigravity_probe","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"Antigravity ordinary text","tool_calls":[{"index":0,"function":{"name":"gravity_probe","arguments":"ok\",\"code\":\"tools.antigravity.read({})\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"total_tokens":1}}`, `[DONE]`,
	}
	for _, fragment := range []int{1, 7, 4096} {
		t.Run(fmt.Sprint(fragment), func(t *testing.T) {
			s := aliasProtocolState(t)
			out := runAliasStream(t, s, events, fragment)
			var calls []any
			seenText := false
			for _, e := range out {
				for _, v := range e["choices"].([]any) {
					c := v.(map[string]any)
					d := c["delta"].(map[string]any)
					if d["content"] == "Antigravity ordinary text" {
						seenText = true
						if len(calls) != 0 {
							t.Fatal("text was buffered behind tool arguments")
						}
					}
					if tools, ok := d["tool_calls"].([]any); ok {
						calls = append(calls, tools...)
					}
				}
			}
			if !seenText || len(calls) != 2 || len(s.calls) != 0 {
				t.Fatal(out)
			}
			first := calls[0].(map[string]any)
			f := first["function"].(map[string]any)
			if f["name"] != "codex_probe" || first["id"] != "call-codex-opaque" || !strings.Contains(f["arguments"].(string), "tools.antigravity.read") || !strings.Contains(f["arguments"].(string), "antigravity_key") {
				t.Fatal(first)
			}
		})
	}
}

func TestAgentAnthropicArgumentStream(t *testing.T) {
	events := []string{`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"codex-id","name":"antigravity_probe","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"antigravity_"}}`, `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Antigravity"}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"key\":\"ok\"}"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_stop"}`}
	out := runAliasStream(t, aliasProtocolState(t), events, 1)
	if len(out) != 5 || out[0]["content_block"].(map[string]any)["name"] != "codex_probe" || out[1]["delta"].(map[string]any)["text"] != "Antigravity" || out[2]["delta"].(map[string]any)["partial_json"] != `{"antigravity_key":"ok"}` {
		t.Fatal(out)
	}
}

func TestAgentResponsesFunctionAndFreeformStreams(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprint(custom), func(t *testing.T) {
			kind, name, field, eventName, text := "function_call", "antigravity_probe", "arguments", "function_call_arguments", `{"antigravity_key":"ok"}`
			if custom {
				kind, name, field, eventName, text = "custom_tool_call", "antigravity_patch", "input", "custom_tool_call_input", "*** Update File: /tmp/antigravity/a.go\n+Antigravity"
			}
			item := map[string]any{"type": kind, "id": "item-id", "call_id": "opaque-codex", "name": name, field: ""}
			encode := func(v any) string { b, _ := json.Marshal(v); return string(b) }
			events := []string{encode(map[string]any{"type": "response.output_item.added", "sequence_number": 0, "item": item})}
			for _, part := range []string{text[:len(text)/2], text[len(text)/2:]} {
				events = append(events, encode(map[string]any{"type": "response." + eventName + ".delta", "sequence_number": len(events), "item_id": "item-id", "delta": part}))
			}
			events = append(events, encode(map[string]any{"type": "response.output_text.delta", "sequence_number": 3, "delta": "Antigravity ordinary answer"}), encode(map[string]any{"type": "response." + eventName + ".done", "sequence_number": 4, "item_id": "item-id", field: text}))
			item[field] = text
			events = append(events, encode(map[string]any{"type": "response.output_item.done", "sequence_number": 5, "item": item}), encode(map[string]any{"type": "response.completed", "sequence_number": 6, "response": map[string]any{"output": []any{item}}}))
			out := runAliasStream(t, aliasProtocolState(t), events, 3)
			if len(out) != 6 {
				t.Fatal(out)
			}
			for i, e := range out {
				if int(e["sequence_number"].(float64)) != i {
					t.Fatal("nonmonotonic sequence", out)
				}
			}
			if out[1]["delta"] != "Antigravity ordinary answer" {
				t.Fatal(out)
			}
			restored := out[2]["delta"].(string)
			if custom {
				if !strings.Contains(restored, "/tmp/antigravity/a.go") || !strings.Contains(restored, "+Antigravity") {
					t.Fatal(restored)
				}
			} else if restored != `{"antigravity_key":"ok"}` {
				t.Fatal(restored)
			}
			if out[0]["item"].(map[string]any)["call_id"] != "opaque-codex" {
				t.Fatal(out)
			}
		})
	}
}

func TestAliasStreamArgumentLimit(t *testing.T) {
	s := aliasProtocolState(t)
	call := &streamAliasCall{}
	if err := s.appendArguments(call, strings.Repeat("x", maxAliasStreamBytes)); err != nil {
		t.Fatal("exact buffer limit rejected", err)
	}
	if err := s.appendArguments(call, "x"); err == nil {
		t.Fatal("argument buffer limit was not enforced")
	}
}
