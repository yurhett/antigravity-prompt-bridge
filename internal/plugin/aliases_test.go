package plugin

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
	"github.com/yurhett/antigravity-prompt-bridge/internal/rewrite"
)

const aliasFixture = `{"systemInstruction":{"parts":[{"text":"You are OpenCode. Use tools.opencode.session_move. /tmp/opencode"}]},"tools":[{"functionDeclarations":[{"name":"opencode_probe","description":"OpenCode","parameters":{"properties":{"opencode_key":{"enum":["opencode"]}}}}]}],"contents":[{"role":"user","parts":[{"text":"OpenCode user data"}]}]}`

func callResult(t *testing.T, p *Plugin, method string, input any, result any) {
	t.Helper()
	raw, _ := json.Marshal(input)
	body, _ := p.Call(method, raw)
	var envelope pluginabi.Envelope
	if err := json.Unmarshal(body, &envelope); err != nil || !envelope.OK {
		t.Fatalf("ABI %s failed: %s", method, body)
	}
	if result != nil {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgentAliasConfigAndProviderScope(t *testing.T) {
	if !config.Default().AgentAlias {
		t.Fatal("agent aliases must be enabled by default")
	}
	if _, err := config.Parse([]byte("agent_alias: invalid")); err == nil {
		t.Fatal("invalid config accepted")
	}
	cfg, err := config.Parse([]byte("agent_alias: true\nheuristic_identity: true\nantigravity_bridge_system_prompt: false\n"))
	if err != nil || !cfg.AgentAlias {
		t.Fatal(cfg, err)
	}
	provider := "antigravity"
	p := New(func(string, any) (json.RawMessage, error) { return providerResponse(provider), nil })
	p.setConfig(cfg)
	request := pluginapi.RequestInterceptRequest{RequestID: "scope-alias", SourceFormat: "gemini", Stream: true, Body: []byte(aliasFixture), Metadata: map[string]any{"selected_auth_index": "test"}}
	defer p.aliases.remove(request.RequestID)
	var response pluginapi.RequestInterceptResponse
	callResult(t, p, pluginabi.MethodRequestInterceptAfter, request, &response)
	if response.Terminate || !strings.Contains(string(response.Body), "antigravity_probe") {
		t.Fatal("Gemini aliasing failed", response)
	}
	request.SourceFormat = "openai"
	callResult(t, p, pluginabi.MethodRequestInterceptAfter, request, &response)
	if response.Terminate {
		t.Fatal("OpenAI streaming must be supported")
	}
	provider = "openai"
	response = pluginapi.RequestInterceptResponse{}
	callResult(t, p, pluginabi.MethodRequestInterceptAfter, request, &response)
	if !reflect.DeepEqual(response, pluginapi.RequestInterceptResponse{}) {
		t.Fatal("unrelated provider modified")
	}
}

func TestAgentAliasConcurrentRequestMapsDoNotCross(t *testing.T) {
	p := New(nil)
	for _, original := range []string{"OpenCode_test", "Opencode_test"} {
		_, state, err := rewriteAliasesForTest([]byte(`{"system":"You are OpenCode.","tools":[{"name":"` + original + `"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := p.aliases.put(original, state); err != nil {
			t.Fatal(err)
		}
		defer p.aliases.remove(original)
	}
	for _, original := range []string{"OpenCode_test", "Opencode_test"} {
		var result pluginapi.ResponseInterceptResponse
		callResult(t, p, pluginabi.MethodResponseInterceptAfter, pluginapi.ResponseInterceptRequest{RequestID: original, Body: []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"Antigravity_test","args":{}}}]}}]}`)}, &result)
		if !strings.Contains(string(result.Body), original) {
			t.Fatalf("request %s used another reverse map: %s", original, result.Body)
		}
	}
}

func TestAgentAliasMultilineSSE(t *testing.T) {
	p := New(nil)
	_, state, err := rewriteAliasesForTest([]byte(aliasFixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.aliases.put("multiline", state); err != nil {
		t.Fatal(err)
	}
	defer p.aliases.remove("multiline")
	var result pluginapi.StreamChunkInterceptResponse
	callResult(t, p, pluginabi.MethodResponseInterceptStreamChunk, pluginapi.StreamChunkInterceptRequest{RequestID: "multiline", Body: []byte("event: message\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"antigravity_probe\",\ndata: \"args\":{}}}]}}]}\n\n")}, &result)
	if result.DropChunk || !strings.Contains(string(result.Body), "opencode_probe") || !strings.Contains(string(result.Body), "event: message") {
		t.Fatalf("bad multiline event: %s", result.Body)
	}
}

func TestAgentAliasRunsBeforeOptionalSystemBridge(t *testing.T) {
	provider := "antigravity"
	p := New(func(string, any) (json.RawMessage, error) { return providerResponse(provider), nil })
	for _, selected := range []string{"agy", "antigravity"} {
		provider = selected
		for _, bridge := range []bool{false, true} {
			cfg := config.Default()
			cfg.HeuristicIdentity, cfg.AgentAlias = true, true
			cfg.AntigravityBridgeSystemPrompt = bridge
			p.setConfig(cfg)
			request := pluginapi.RequestInterceptRequest{RequestID: "alias-before-bridge", SourceFormat: "gemini", Stream: true, Metadata: map[string]any{"selected_auth_index": "test"}, Body: []byte(`{"systemInstruction":{"parts":[{"text":"You are OpenCode. Tools for OpenCode: opencode_read."}]},"tools":[{"functionDeclarations":[{"name":"opencode_read","description":"OpenCode"}]}],"contents":[{"role":"user","parts":[{"text":"OpenCode user"}]}]}`)}
			var response pluginapi.RequestInterceptResponse
			callResult(t, p, pluginabi.MethodRequestInterceptAfter, request, &response)
			if response.Terminate {
				t.Fatalf("request terminated: %s", response.ResponseBody)
			}
			got, _ := protocol.DecodeJSON(response.Body)
			root := got.(map[string]any)
			contents := root["contents"].([]any)
			if contents[len(contents)-1].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"] != "OpenCode user" {
				t.Fatalf("user text changed: %s", response.Body)
			}
			if selected == "antigravity" && bridge {
				moved := contents[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
				if !strings.Contains(moved, "Tools for Antigravity: antigravity_read") || strings.Contains(moved, "Tools for OpenCode:") {
					t.Fatalf("system was moved before aliasing: %s", response.Body)
				}
			} else if len(contents) != 1 || !strings.Contains(string(response.Body), "Tools for Antigravity: antigravity_read") {
				t.Fatalf("unexpected bridge/provider behavior: %s", response.Body)
			}
			p.aliases.remove(request.RequestID)
		}
	}
}

func providerResponse(provider string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"auth": map[string]any{"provider": provider}})
	return raw
}

func rewriteAliasesForTest(body []byte) ([]byte, *rewrite.AliasState, error) {
	result, err := rewrite.Request(body, config.Default(), "gemini")
	return result.Body, result.State, err
}

func TestPluginInstancesIsolateConfigurationAndAliases(t *testing.T) {
	host := func(string, any) (json.RawMessage, error) { return providerResponse("antigravity"), nil }
	first, second := New(host), New(host)
	const requestID = "shared-request-id"

	callResult(t, first, pluginabi.MethodPluginReconfigure, lifecycleRequest{
		ConfigYAML: []byte("antigravity_bridge_system_prompt: false\n"),
	}, nil)
	intercept := func(p *Plugin, tool string) pluginapi.RequestInterceptResponse {
		t.Helper()
		var response pluginapi.RequestInterceptResponse
		callResult(t, p, pluginabi.MethodRequestInterceptAfter, pluginapi.RequestInterceptRequest{
			RequestID: requestID, Metadata: map[string]any{"selected_auth_index": "test"},
			Body: []byte(`{"messages":[{"role":"system","content":"You are OpenCode."},{"role":"user","content":"Read"}],"tools":[{"type":"function","function":{"name":"` + tool + `"}}]}`),
		}, &response)
		return response
	}
	for _, tc := range []struct {
		plugin *Plugin
		name   string
		bridge bool
	}{{first, "OpenCode_test", false}, {second, "Opencode_test", true}} {
		response := intercept(tc.plugin, tc.name)
		if response.Terminate || !strings.Contains(string(response.Body), "Antigravity_test") ||
			strings.Contains(string(response.Body), `\u003cUSER_REQUEST\u003e`) != tc.bridge {
			t.Fatalf("incorrect rewrite for %s: %s", tc.name, response.Body)
		}
	}
	restore := func(p *Plugin) string {
		t.Helper()
		var response pluginapi.ResponseInterceptResponse
		callResult(t, p, pluginabi.MethodResponseInterceptAfter, pluginapi.ResponseInterceptRequest{
			RequestID: requestID,
			Body:      []byte(`{"choices":[{"message":{"tool_calls":[{"function":{"name":"Antigravity_test","arguments":"{}"}}]}}]}`),
		}, &response)
		return string(response.Body)
	}
	if body := restore(first); !strings.Contains(body, "OpenCode_test") {
		t.Fatalf("first instance returned the wrong alias: %s", body)
	}
	first.Shutdown()
	if body := restore(first); body != "" {
		t.Fatalf("first instance retained aliases after shutdown: %s", body)
	}
	if body := restore(second); !strings.Contains(body, "Opencode_test") {
		t.Fatalf("second instance lost its alias: %s", body)
	}
}
