package plugin

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
)

func TestAgentPromptRuntimeFallbackAuditAndStatelessRewrite(t *testing.T) {
	var logs []map[string]any
	provider := "agy"
	p := New(func(method string, payload any) (json.RawMessage, error) {
		if method == "host.log" {
			logs = append(logs, payload.(map[string]any)["fields"].(map[string]any))
		}
		return json.Marshal(map[string]any{"auth": map[string]any{"provider": provider}})
	})
	intercept := func(body string) pluginapi.RequestInterceptResponse {
		t.Helper()
		request := pluginapi.RequestInterceptRequest{SourceFormat: "unused-format", Stream: true, Metadata: map[string]any{"selected_auth_index": "test"}, Body: []byte(body)}
		var response pluginapi.RequestInterceptResponse
		callResult(t, p, pluginabi.MethodRequestInterceptAfter, request, &response)
		if response.Terminate {
			t.Fatalf("request terminated: %s", response.ResponseBody)
		}
		return response
	}
	cfg := config.Default()
	cfg.Audit = true
	cfg.HeuristicIdentity = true
	cfg.CustomMappings = []config.Mapping{{Match: "local_marker", Replacement: "project notes"}}
	p.setConfig(cfg)

	// A quoted identity must not enable aliases. The old identity and custom
	// mapping logic still run for the unrecognized Nova declaration.
	legacy := intercept(`{"system":"Example: ` + "`You are OpenCode.`" + `\nYou are Nova, an AI assistant created by Nova Labs. Keep local_marker.","tools":[{"name":"opencode_probe"}]}`)
	if !strings.Contains(string(legacy.Body), "You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team. Keep project notes.") || !strings.Contains(string(legacy.Body), `"name":"opencode_probe"`) || logs[0]["agent_families"] != nil {
		t.Fatalf("wrong legacy rewrite: %s (%v)", legacy.Body, logs)
	}

	provider = "antigravity"
	stateless := intercept(`{"systemInstruction":{"parts":[{"text":"You are a coding assistant running inside pi, a coding agent harness. Pi agent reads local_marker. ` + "`pi --mode json`" + `"}]},"tools":[{"functionDeclarations":[{"name":"read","description":"pi can read files","parameters":{"properties":{"value":{"description":"pi number","default":9007199254740993}}}}]}],"contents":[{"role":"user","parts":[{"text":"do the task"}]}]}`)
	if !strings.Contains(string(stateless.Body), "Antigravity agent reads project notes.") || !strings.Contains(string(stateless.Body), "9007199254740993") || p.aliases.get("") != nil {
		t.Fatalf("wrong stateless bridge: %s", stateless.Body)
	}
	audit := logs[len(logs)-1]
	if !reflect.DeepEqual(audit["agent_families"], []string{"Pi"}) || audit["template_hits"] != 1 || audit["residual_fixed"] != 3 || audit["residual_protected"] != 1 {
		t.Fatalf("wrong audit: %v", audit)
	}
	encoded, _ := json.Marshal(audit)
	if strings.Contains(string(encoded), "project notes") || strings.Contains(string(encoded), "pi --mode") {
		t.Fatal("logged prompt contents", string(encoded))
	}

	provider = "agy"
	cfg.AgentAlias, cfg.HeuristicIdentity = false, false
	p.setConfig(cfg)
	disabled := intercept(`{"system":"You are OpenCode. Use local_marker.","tools":[{"name":"opencode_probe","description":"OpenCode"}]}`)
	if !strings.Contains(string(disabled.Body), "You are Antigravity. Use project notes.") || !strings.Contains(string(disabled.Body), `"description":"OpenCode","name":"opencode_probe"`) {
		t.Fatalf("wrong agent_alias off behavior: %s", disabled.Body)
	}
}
