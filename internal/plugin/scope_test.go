package plugin

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
)

func TestScopeUsesSelectedCredentialRatherThanModelOrWireFormat(t *testing.T) {
	p := New(nil)
	cfg := config.Default()
	cfg.AgentAlias = false                    // Isolate provider/identity handling.
	cfg.AntigravityBridgeSystemPrompt = false // This test isolates rewrite/provider selection.
	p.setConfig(cfg)
	for _, tt := range []struct {
		name, provider, index, model, format string
		changed                              bool
	}{
		{"prefixed agy", "agy", "idx", "agy/gemini", "openai", true},
		{"unprefixed alias", "agy", "idx", "my-alias", "openai", true},
		{"Gemini downstream", "agy", "idx", "gemini", "openai", true},
		{"native antigravity", "antigravity", "idx", "gemini", "antigravity", true},
		{"OpenAI model named agy", "openai", "idx", "agy/test", "openai", false},
		{"unknown selected auth", "", "idx", "agy/test", "openai", false},
		{"no selected auth", "agy", "", "agy/test", "openai", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p.hostCall = func(method string, payload any) (json.RawMessage, error) {
				if method != "host.auth.get_runtime" {
					t.Fatalf("unexpected callback %s", method)
				}
				if payload.(map[string]any)["auth_index"] != tt.index {
					t.Fatal("wrong selected credential")
				}
				b, _ := json.Marshal(map[string]any{"auth": map[string]any{"provider": tt.provider}})
				return b, nil
			}
			req := pluginapi.RequestInterceptRequest{ToFormat: tt.format, Model: tt.model, Metadata: map[string]any{"selected_auth_index": tt.index}, Body: []byte(`{"messages":[{"role":"system","content":"You are OpenCode."},{"role":"developer","content":"Use Claude Code."},{"role":"user","content":"OpenCode user"}],"tools":[{"type":"function","function":{"name":"Codex","description":"OpenCode","parameters":{"type":"object"}}}]}`)}
			raw, _ := json.Marshal(req)
			var env pluginabi.Envelope
			json.Unmarshal(p.handleRequestAfter(raw), &env)
			var response pluginapi.RequestInterceptResponse
			json.Unmarshal(env.Result, &response)
			if (len(response.Body) > 0) != tt.changed {
				t.Fatalf("changed=%v want %v: %s", len(response.Body) > 0, tt.changed, env.Result)
			}
			if tt.changed {
				var got map[string]any
				json.Unmarshal(response.Body, &got)
				messages := got["messages"].([]any)
				if messages[0].(map[string]any)["content"] != "You are Antigravity." || messages[1].(map[string]any)["content"] != "Use Antigravity." {
					t.Fatalf("identity not rewritten: %s", response.Body)
				}
				var before map[string]any
				json.Unmarshal(req.Body, &before)
				if !reflect.DeepEqual(got["tools"], before["tools"]) || !reflect.DeepEqual(messages[2], before["messages"].([]any)[2]) {
					t.Fatal("user/tools modified")
				}
			}
		})
	}
}

func TestRegistersOnlyInterceptorAndRejectsBlockMode(t *testing.T) {
	p := New(nil)
	var env pluginabi.Envelope
	json.Unmarshal(p.handleLifecycle(nil), &env)
	var result struct {
		Capabilities map[string]bool    `json:"capabilities"`
		Metadata     pluginapi.Metadata `json:"metadata"`
	}
	json.Unmarshal(env.Result, &result)
	if result.Metadata.Name == "" || result.Metadata.Version == "" || result.Metadata.Author == "" || result.Metadata.GitHubRepository == "" {
		t.Fatal("Missing CPA-required metadata")
	}
	if len(result.Capabilities) != 4 || !result.Capabilities["request_interceptor"] || !result.Capabilities["response_interceptor"] || !result.Capabilities["response_stream_interceptor"] || !result.Capabilities["request_lifecycle_plugin"] {
		t.Fatal(result.Capabilities)
	}
	if _, err := config.Parse([]byte("mode: block")); err == nil {
		t.Fatal("block must be rejected")
	}
}
func TestProviderSwitchesDefaultOnAndAreIndependentAtRuntime(t *testing.T) {
	p := New(nil)
	defaults, err := config.Parse(nil)
	if err != nil || !defaults.EnableAgy || !defaults.EnableAntigravity {
		t.Fatal("both providers must default on")
	}
	for _, key := range []string{"enable_agy", "enable_antigravity", "heuristic_identity"} {
		if _, err := config.Parse([]byte(key + ": invalid")); err == nil {
			t.Fatalf("accepted non-boolean %s", key)
		}
	}
	for _, agy := range []bool{false, true} {
		for _, native := range []bool{false, true} {
			cfg, err := config.Parse([]byte(fmt.Sprintf("enable_agy: %v\nenable_antigravity: %v\nheuristic_identity: true", agy, native)))
			if err != nil {
				t.Fatal(err)
			}
			p.setConfig(cfg)
			for _, provider := range []string{"agy", "antigravity", " AGY ", "openai", "gemini", ""} {
				p.hostCall = func(string, any) (json.RawMessage, error) {
					b, _ := json.Marshal(map[string]any{"auth": map[string]any{"provider": provider}})
					return b, nil
				}
				req := pluginapi.RequestInterceptRequest{Model: "caller-controlled-model", Metadata: map[string]any{"selected_auth_index": "idx"}, Body: []byte(`{"system":"You are NovaCode, an AI coding assistant."}`)}
				b, _ := json.Marshal(req)
				var env pluginabi.Envelope
				json.Unmarshal(p.handleRequestAfter(b), &env)
				var response pluginapi.RequestInterceptResponse
				json.Unmarshal(env.Result, &response)
				want := agy && strings.EqualFold(strings.TrimSpace(provider), "agy") || native && provider == "antigravity"
				if (len(response.Body) > 0) != want {
					t.Fatalf("agy=%v native=%v provider=%q: body=%s", agy, native, provider, response.Body)
				}
			}
		}
	}
}
