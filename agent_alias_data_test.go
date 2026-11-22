package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const pluginSourceSample = "tools.opencode.read tools.antigravity.read Continue.dev Refact.ai OpenCode Codex /tmp/opencode /tmp/antigravity"

func TestAgentAliasExactFieldWhitelist(t *testing.T) {
	payload := map[string]any{"name": "codex_probe", "system": "OpenCode", "tools": []any{map[string]any{"name": "codex_probe"}}, "codex_key": "Codex", "namespace": "opencode", "path": "/tmp/opencode", "source": pluginSourceSample}
	rawArgs := `{ "codex_key" : "OpenCode", "code":"tools.opencode.read(); tools.antigravity.read();", "path":"/tmp/opencode" }`
	for _, format := range []string{"gemini", "chat", "responses", "anthropic"} {
		t.Run(format, func(t *testing.T) {
			root := map[string]any{"system": "You are OpenCode. Use codex_probe and see tools.opencode.read /tmp/opencode", "metadata": payload}
			argument := map[string]any{"description": "OpenCode uses `codex_probe` and Codex supports its own tools.", "enum": []any{"OpenCode"}, "default": map[string]any{"description": "OpenCode"}}
			schema := map[string]any{"description": "OpenCode schema.", "properties": map[string]any{"opencode_key": map[string]any{"items": map[string]any{"anyOf": []any{argument}}}}}
			schemaKey := "parameters"
			if format == "anthropic" {
				schemaKey = "input_schema"
			} else if format == "gemini" {
				schemaKey = "parametersJsonSchema"
			}
			definition := map[string]any{"name": "codex_probe", "description": pluginSourceSample, schemaKey: schema}
			var call, result map[string]any
			switch format {
			case "gemini":
				root["tools"] = []any{map[string]any{"functionDeclarations": []any{definition}}}
				root["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"allowedFunctionNames": []any{"codex_probe"}}}
				call = map[string]any{"name": "codex_probe", "id": "codex-id", "args": payload}
				result = map[string]any{"name": "codex_probe", "id": "codex-id", "response": payload}
				root["contents"] = []any{map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": call, "thoughtSignature": "codex-sig"}}}, map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": result}, map[string]any{"text": pluginSourceSample}}}}
			case "chat":
				root["tools"] = []any{map[string]any{"type": "function", "function": definition}}
				root["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "codex_probe"}}
				call = map[string]any{"name": "codex_probe", "arguments": rawArgs}
				result = map[string]any{"role": "tool", "name": "codex_probe", "tool_call_id": "codex-id", "content": pluginSourceSample}
				root["messages"] = []any{map[string]any{"role": "assistant", "content": pluginSourceSample, "tool_calls": []any{map[string]any{"id": "codex-id", "function": call}}}, result, map[string]any{"role": "user", "content": pluginSourceSample}}
			case "responses":
				definition["type"] = "function"
				root["tools"] = []any{definition}
				root["tool_choice"] = map[string]any{"type": "function", "name": "codex_probe"}
				call = map[string]any{"type": "function_call", "name": "codex_probe", "call_id": "codex-id", "arguments": rawArgs}
				result = map[string]any{"type": "function_call_output", "call_id": "codex-id", "output": payload}
				root["input"] = []any{call, result, map[string]any{"role": "user", "content": pluginSourceSample}}
			case "anthropic":
				root["tools"] = []any{definition}
				root["tool_choice"] = map[string]any{"type": "tool", "name": "codex_probe"}
				call = map[string]any{"type": "tool_use", "name": "codex_probe", "id": "codex-id", "input": payload}
				result = map[string]any{"type": "tool_result", "tool_use_id": "codex-id", "content": []any{map[string]any{"type": "text", "text": pluginSourceSample}, map[string]any{"type": "image", "source": payload}}}
				root["messages"] = []any{map[string]any{"role": "assistant", "content": []any{call, map[string]any{"type": "text", "text": pluginSourceSample}}}, map[string]any{"role": "user", "content": []any{result, map[string]any{"type": "text", "text": pluginSourceSample}}}}
			}
			before, _ := json.Marshal(root)
			body, state, err := aliasAgentRequest(before)
			if err != nil || state == nil {
				t.Fatal(err)
			}
			// Construct the only allowed differences by exact protocol slots.
			expected, _ := decodeAliasJSON(before)
			e := expected.(map[string]any)
			setName := func(v any) { v.(map[string]any)["name"] = "antigravity_probe" }
			setDecl := func(v any) {
				setName(v)
				tool := v.(map[string]any)
				tool["description"] = strings.Replace(pluginSourceSample, "OpenCode Codex", "Antigravity Codex", 1)
				s := tool[schemaKey].(map[string]any)
				s["description"] = "Antigravity schema."
				property := s["properties"].(map[string]any)["opencode_key"].(map[string]any)
				item := property["items"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)
				item["description"] = "Antigravity uses `antigravity_probe` and Codex supports its own tools."
			}
			tools := e["tools"].([]any)
			switch format {
			case "gemini":
				setDecl(tools[0].(map[string]any)["functionDeclarations"].([]any)[0])
				e["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)["allowedFunctionNames"].([]any)[0] = "antigravity_probe"
				contents := e["contents"].([]any)
				setName(contents[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionCall"])
				setName(contents[1].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"])
			case "chat":
				setDecl(tools[0].(map[string]any)["function"])
				setName(e["tool_choice"].(map[string]any)["function"])
				messages := e["messages"].([]any)
				setName(messages[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["function"])
				setName(messages[1])
			case "responses":
				setDecl(tools[0])
				setName(e["tool_choice"])
				setName(e["input"].([]any)[0])
			case "anthropic":
				setDecl(tools[0])
				setName(e["tool_choice"])
				setName(e["messages"].([]any)[0].(map[string]any)["content"].([]any)[0])
			}
			expected.(map[string]any)["system"] = nativeAntigravityIdentity + " Use antigravity_probe and see tools.opencode.read /tmp/opencode"
			got, _ := decodeAliasJSON(body)
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("changed data outside whitelist: %s", body)
			}
			// Malformed or freeform arguments are data; do not parse or normalize them.
			for _, args := range []any{payload, rawArgs, "not JSON: " + pluginSourceSample} {
				f := map[string]any{"name": "antigravity_probe", "arguments": args}
				if err := state.restoreFunction(f); err != nil {
					t.Fatal(err)
				}
				if f["name"] != "codex_probe" || !reflect.DeepEqual(f["arguments"], args) {
					t.Fatal(f)
				}
			}
			repeated, s, err := aliasAgentRequest(body)
			if err != nil || s != nil || repeated != nil {
				t.Fatal("not idempotent", err)
			}
		})
	}
}
func TestAgentPluginPathsAndNamespaceExamplesPreserved(t *testing.T) {
	for _, path := range []string{"/home/u/.config/opencode/plugins/opencode-probe.ts", ".opencode/plugin/codex.ts", "plugins/codex.so", `C:\Users\u\.config\opencode\plugins\codex.ts`, "/work/antigravity-prompt-bridge/opencode_alias_test.go"} {
		raw, _ := json.Marshal(map[string]any{"system": "You are OpenCode agent: " + path, "tools": []any{map[string]any{"name": "read"}}, "contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "read", "response": map[string]any{"output": pluginSourceSample}}}}}}})
		body, _, err := aliasAgentRequest(raw)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := decodeAliasJSON(body)
		if v.(map[string]any)["system"] != "You are Antigravity agent: "+path {
			t.Fatal(string(body))
		}
	}
	for _, text := range []string{"Continue.dev Refact.ai", "tools.opencode.read tools.antigravity.read", "/tmp/opencode /tmp/antigravity"} {
		raw, _ := json.Marshal(map[string]any{"system": text})
		if _, _, err := aliasAgentRequest(raw); err != nil {
			t.Fatal("prose is not a tool registration conflict", err)
		}
	}
}
func TestAgentOnlyNativeToolNamesCanConflict(t *testing.T) {
	for _, input := range []string{`{"system":"You are Codex.","tools":[{"name":"codex_probe"},{"name":"antigravity_probe"}]}`, `{"system":"You are Codex.","tools":[{"type":"function","function":{"name":"codex_probe"}},{"type":"function","function":{"name":"opencode_probe"}}]}`} {
		if _, _, err := aliasAgentRequest([]byte(input)); err == nil {
			t.Fatal("actual native conflict accepted")
		}
	}
	// Native namespaces are executable identity; freeform grammars stay unchanged.
	raw := `{"system":"You are Codex.","tools":[{"type":"namespace","name":"codex","tools":[{"type":"custom","name":"codex_patch","format":{"type":"grammar","definition":"OpenCode Codex tools.opencode tools.antigravity"}}]}]}`
	body, s, err := aliasAgentRequest([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"name":"antigravity"`) || !strings.Contains(string(body), `"name":"antigravity_patch"`) {
		t.Fatal(string(body))
	}
	f := map[string]any{"namespace": "codex", "name": "antigravity_patch", "input": pluginSourceSample}
	if err := s.restoreFunction(f); err != nil {
		t.Fatal(err)
	}
	if f["namespace"] != "codex" || f["name"] != "codex_patch" || f["input"] != pluginSourceSample {
		t.Fatal(f)
	}
}

func TestAgentNamesPreserveStreamDataAndMetadata(t *testing.T) {
	s := aliasProtocolState(t)
	raw := `{ "OpenCode" : "tools.opencode tools.antigravity", "path": "/tmp/codex" }`
	first, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "keep-id", "signature": "OpenCode-signature", "type": "function", "function": map[string]any{"name": "antigravity_probe", "arguments": raw[:12], "opaque": "Codex-function-metadata"}}}}}}})
	second, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": raw[12:]}}}}}}})
	events := runAliasStream(t, s, []string{string(first), string(second), `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`}, 1)
	found := false
	for _, e := range events {
		choices, _ := e["choices"].([]any)
		for _, v := range choices {
			delta, _ := v.(map[string]any)["delta"].(map[string]any)
			calls, _ := delta["tool_calls"].([]any)
			for _, v := range calls {
				call := v.(map[string]any)
				f := call["function"].(map[string]any)
				if f["name"] != "codex_probe" || f["arguments"] != raw || f["opaque"] != "Codex-function-metadata" || call["signature"] != "OpenCode-signature" {
					t.Fatal(call)
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatal("no restored tool call")
	}
}

func TestAgentNonToolSelectorsPreserved(t *testing.T) {
	for _, kind := range []string{"web_search", "unknown"} {
		root := map[string]any{"system": "You are Codex.", "tool_choice": map[string]any{"type": kind, "name": "codex", "function": map[string]any{"name": "codex_probe"}}}
		original, _ := json.Marshal(root)
		body, _, err := aliasAgentRequest(original)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := decodeAliasJSON(body)
		if !reflect.DeepEqual(got.(map[string]any)["tool_choice"], root["tool_choice"]) {
			t.Fatal(string(body))
		}
	}
}
