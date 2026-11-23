package plugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
)

func TestNativeBridgeDefaultsAndProviderIsolation(t *testing.T) {
	cfg, err := config.Parse([]byte("heuristic_identity: true"))
	if err != nil || !cfg.AntigravityBridgeSystemPrompt {
		t.Fatal("bridge must default on", err)
	}
	if _, err := config.Parse([]byte("antigravity_bridge_system_prompt: invalid")); err == nil {
		t.Fatal("invalid switch accepted")
	}
	input := []byte(`{"messages":[{"role":"system","content":"You are an AI agent running in OpenCode, a coding agent harness.\nKeep all rules."},{"role":"user","content":"QUESTION"}]}`)
	var provider string
	p := New(func(string, any) (json.RawMessage, error) { return providerResponse(provider), nil })
	rewriteProvider := func(body []byte, cfg config.Config, selected string) ([]byte, bool) {
		provider = selected
		cfg.AgentAlias = false
		p.setConfig(cfg)
		var response pluginapi.RequestInterceptResponse
		callResult(t, p, pluginabi.MethodRequestInterceptAfter, pluginapi.RequestInterceptRequest{
			Metadata: map[string]any{"selected_auth_index": "test"}, Body: body,
		}, &response)
		return response.Body, response.Body != nil
	}
	for _, provider := range []string{"agy", " AGY ", "openai", "antigravity", " Antigravity "} {
		on, changed := rewriteProvider(input, cfg, provider)
		isNative := strings.EqualFold(strings.TrimSpace(provider), "antigravity")
		cfg.AntigravityBridgeSystemPrompt = false
		off, changedOff := rewriteProvider(input, cfg, provider)
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
	if _, changed := rewriteProvider(input, cfg, "antigravity"); changed {
		t.Fatal("disabled provider modified")
	}
	p.setConfig(cfg)
	if p.configSnapshot().AntigravityBridgeSystemPrompt != cfg.AntigravityBridgeSystemPrompt {
		t.Fatal("switch lost in config copy")
	}
}
