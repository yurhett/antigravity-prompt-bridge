package main

import (
	"encoding/json"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const aliasFixture = `{"systemInstruction":{"parts":[{"text":"You are OpenCode. Use tools.opencode.session_move. /tmp/opencode"}]},"tools":[{"functionDeclarations":[{"name":"opencode_probe","description":"OpenCode","parameters":{"properties":{"opencode_key":{"enum":["opencode"]}}}}]}],"contents":[{"role":"user","parts":[{"text":"OpenCode user data"}]}]}`

func aliasCallResult(t *testing.T, method string, input any, result any) {
	t.Helper()
	raw, _ := json.Marshal(input)
	body, _ := handlePluginCall(method, raw)
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

func TestAgentAliasIgnoresUnchangedAndMalformedBodies(t *testing.T) {
	for _, body := range []string{`{"text":"Antigravity"}`, `{`, `{"text":"opencode"} trailing`} {
		if _, state, err := aliasAgentRequest([]byte(body)); state != nil || err != nil {
			t.Errorf("unexpected alias result for %s: %v %v", body, state, err)
		}
	}
}

func TestAgentAliasConfigAndProviderScope(t *testing.T) {
	defer restoreDefaultFilterConfig(t)
	if !defaultFilterConfig().AgentAlias {
		t.Fatal("agent aliases must be enabled by default")
	}
	if _, err := parseFilterConfigYAML([]byte("agent_alias: invalid")); err == nil {
		t.Fatal("invalid config accepted")
	}
	cfg, err := parseFilterConfigYAML([]byte("agent_alias: true\nheuristic_identity: true\nantigravity_bridge_system_prompt: false\n"))
	if err != nil || !cfg.AgentAlias {
		t.Fatal(cfg, err)
	}
	applyFilterConfig(cfg)
	old := hostCall
	defer func() { hostCall = old }()
	provider := "antigravity"
	hostCall = func(method string, payload any) (json.RawMessage, error) {
		raw, _ := json.Marshal(map[string]any{"auth": map[string]any{"provider": provider}})
		return raw, nil
	}
	request := pluginapi.RequestInterceptRequest{RequestID: "scope-alias", SourceFormat: "gemini", Stream: true, Body: []byte(aliasFixture), Metadata: map[string]any{"selected_auth_index": "test"}}
	defer agentAliases.remove(request.RequestID)
	var response pluginapi.RequestInterceptResponse
	aliasCallResult(t, pluginabi.MethodRequestInterceptAfter, request, &response)
	if response.Terminate || !strings.Contains(string(response.Body), "antigravity_probe") {
		t.Fatal("Gemini aliasing failed", response)
	}
	request.SourceFormat = "openai"
	aliasCallResult(t, pluginabi.MethodRequestInterceptAfter, request, &response)
	if response.Terminate {
		t.Fatal("OpenAI streaming must be supported")
	}
	provider = "openai"
	response = pluginapi.RequestInterceptResponse{}
	aliasCallResult(t, pluginabi.MethodRequestInterceptAfter, request, &response)
	if !reflect.DeepEqual(response, pluginapi.RequestInterceptResponse{}) {
		t.Fatal("unrelated provider modified")
	}
}

func TestAgentAliasStateIsolationConcurrent(t *testing.T) {
	var workers sync.WaitGroup
	for i := 0; i < 24; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, state, err := aliasAgentRequest([]byte(aliasFixture))
			if err != nil {
				t.Error(err)
				return
			}
			value, _ := decodeAliasJSON([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"antigravity_probe","args":{"namespace":"antigravity"}}}]}}]}`))
			state.restoreResponse(value)
		}()
	}
	workers.Wait()
}

func TestAgentAliasConcurrentRequestMapsDoNotCross(t *testing.T) {
	for _, original := range []string{"OpenCode_test", "Opencode_test"} {
		_, state, err := aliasAgentRequest([]byte(`{"system":"You are OpenCode.","tools":[{"name":"` + original + `"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := agentAliases.put(original, state); err != nil {
			t.Fatal(err)
		}
		defer agentAliases.remove(original)
	}
	for _, original := range []string{"OpenCode_test", "Opencode_test"} {
		var result pluginapi.ResponseInterceptResponse
		aliasCallResult(t, pluginabi.MethodResponseInterceptAfter, pluginapi.ResponseInterceptRequest{RequestID: original, Body: []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"Antigravity_test","args":{}}}]}}]}`)}, &result)
		if !strings.Contains(string(result.Body), original) {
			t.Fatalf("request %s used another reverse map: %s", original, result.Body)
		}
	}
}

func TestAgentAliasMultilineSSE(t *testing.T) {
	_, state, err := aliasAgentRequest([]byte(aliasFixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := agentAliases.put("multiline", state); err != nil {
		t.Fatal(err)
	}
	defer agentAliases.remove("multiline")
	var result pluginapi.StreamChunkInterceptResponse
	aliasCallResult(t, pluginabi.MethodResponseInterceptStreamChunk, pluginapi.StreamChunkInterceptRequest{RequestID: "multiline", Body: []byte("event: message\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"antigravity_probe\",\ndata: \"args\":{}}}]}}]}\n\n")}, &result)
	if result.DropChunk || !strings.Contains(string(result.Body), "opencode_probe") || !strings.Contains(string(result.Body), "event: message") {
		t.Fatalf("bad multiline event: %s", result.Body)
	}
}

func TestAgentAliasRunsBeforeOptionalSystemBridge(t *testing.T) {
	defer restoreDefaultFilterConfig(t)
	old := hostCall
	defer func() { hostCall = old }()
	provider := "antigravity"
	hostCall = func(method string, payload any) (json.RawMessage, error) {
		raw, _ := json.Marshal(map[string]any{"auth": map[string]any{"provider": provider}})
		return raw, nil
	}
	for _, p := range []string{"agy", "antigravity"} {
		provider = p
		for _, bridge := range []bool{false, true} {
			cfg := defaultFilterConfig()
			cfg.HeuristicIdentity, cfg.AgentAlias = true, true
			cfg.AntigravityBridgeSystemPrompt = bridge
			applyFilterConfig(cfg)
			request := pluginapi.RequestInterceptRequest{RequestID: "alias-before-bridge", SourceFormat: "gemini", Stream: true, Metadata: map[string]any{"selected_auth_index": "test"}, Body: []byte(`{"systemInstruction":{"parts":[{"text":"You are OpenCode. Tools for OpenCode: opencode_read."}]},"tools":[{"functionDeclarations":[{"name":"opencode_read","description":"OpenCode"}]}],"contents":[{"role":"user","parts":[{"text":"OpenCode user"}]}]}`)}
			var response pluginapi.RequestInterceptResponse
			aliasCallResult(t, pluginabi.MethodRequestInterceptAfter, request, &response)
			if response.Terminate {
				t.Fatalf("request terminated: %s", response.ResponseBody)
			}
			got, _ := decodeAliasJSON(response.Body)
			root := got.(map[string]any)
			contents := root["contents"].([]any)
			if contents[len(contents)-1].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"] != "OpenCode user" {
				t.Fatalf("user text changed: %s", response.Body)
			}
			if p == "antigravity" && bridge {
				moved := contents[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
				if !strings.Contains(moved, "Tools for Antigravity: antigravity_read") || strings.Contains(moved, "Tools for OpenCode:") {
					t.Fatalf("system was moved before aliasing: %s", response.Body)
				}
			} else if len(contents) != 1 || !strings.Contains(string(response.Body), "Tools for Antigravity: antigravity_read") {
				t.Fatalf("unexpected bridge/provider behavior: %s", response.Body)
			}
			agentAliases.remove(request.RequestID)
		}
	}
}
