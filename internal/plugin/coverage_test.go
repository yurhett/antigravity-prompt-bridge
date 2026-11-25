package plugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
)

func TestBrandGuardModes(t *testing.T) {
	var warnings []string
	p := New(func(method string, payload any) (json.RawMessage, error) {
		if method == "host.log" {
			if fields := payload.(map[string]any); fields["level"] == "warn" {
				warnings = append(warnings, fields["message"].(string))
			}
		}
		return providerResponse("agy"), nil
	})
	intercept := func(mode config.GuardMode, body string) pluginapi.RequestInterceptResponse {
		t.Helper()
		warnings = nil
		cfg := config.Default()
		cfg.BrandGuard = mode
		p.setConfig(cfg)
		var response pluginapi.RequestInterceptResponse
		callResult(t, p, pluginabi.MethodRequestInterceptAfter, pluginapi.RequestInterceptRequest{
			RequestID: "guard", Metadata: map[string]any{"selected_auth_index": "test"}, Body: []byte(body),
		}, &response)
		return response
	}
	// Drift: an unrecorded OpenCode self-introduction. Normalization replaces
	// it, so only the recorded template is out of date.
	drifted := `{"messages":[{"role":"system","content":"You are OpenCode, the open source coding agent."},{"role":"user","content":"hi"}]}`
	if r := intercept(config.GuardOff, drifted); r.Terminate || len(warnings) != 0 {
		t.Fatalf("off: %+v %v", r, warnings)
	}
	for _, mode := range []config.GuardMode{config.GuardReport, config.GuardReject} {
		if r := intercept(mode, drifted); r.Terminate || len(warnings) != 1 || !strings.Contains(warnings[0], "drift identity-template:OpenCode (0/1)") {
			t.Fatalf("%s drift: %+v %v", mode, r, warnings)
		}
	}
	r := intercept(config.GuardStrict, drifted)
	if !r.Terminate || r.StatusCode != 400 || !strings.Contains(string(r.ResponseBody), `"code":"agent_bridge_coverage_gap"`) ||
		!strings.Contains(string(r.ResponseBody), `"rule":"identity-template:OpenCode"`) {
		t.Fatalf("strict forwarded drift: %+v %s", r, r.ResponseBody)
	}

	// Gap: Code Mode is declared but its catalog only appears in an example.
	gap := `{"system":"You are an AI agent running in OpenCode, a coding agent harness.\n` + "```text\\n- tools.opencode.read(): Promise<{}>\\n```" + `","tools":[{"name":"execute","description":"Run code.","input_schema":{"type":"object","properties":{"code":{"type":"string"}}}}],"messages":[{"role":"user","content":"hi"}]}`
	if r := intercept(config.GuardReport, gap); r.Terminate || len(warnings) != 1 || !strings.Contains(warnings[0], "gap adapter:opencode-code-mode") {
		t.Fatalf("report gap: %+v %v", r, warnings)
	}
	if r := intercept(config.GuardReject, gap); !r.Terminate || !strings.Contains(string(r.ResponseBody), `"rule":"adapter:opencode-code-mode"`) {
		t.Fatalf("reject forwarded a gap: %+v %s", r, r.ResponseBody)
	}

	recorded := `{"messages":[{"role":"system","content":"You are an AI agent running in OpenCode, a coding agent harness."},{"role":"user","content":"hi"}]}`
	for _, mode := range []config.GuardMode{config.GuardReject, config.GuardStrict} {
		if r := intercept(mode, recorded); r.Terminate || len(warnings) != 0 {
			t.Fatalf("%s refused a covered request: %s %v", mode, r.ResponseBody, warnings)
		}
	}
}

func TestUnidentifiedModes(t *testing.T) {
	p := New(func(string, any) (json.RawMessage, error) { return providerResponse("agy"), nil })
	intercept := func(cfg config.Config, format, body string) pluginapi.RequestInterceptResponse {
		t.Helper()
		p.setConfig(cfg)
		var response pluginapi.RequestInterceptResponse
		callResult(t, p, pluginabi.MethodRequestInterceptAfter, pluginapi.RequestInterceptRequest{
			RequestID: "unidentified", SourceFormat: format, Metadata: map[string]any{"selected_auth_index": "test"}, Body: []byte(body),
		}, &response)
		return response
	}
	legacy := config.Default()
	legacy.Unidentified = config.UnidentifiedLegacy
	aliasOff := config.Default()
	aliasOff.AgentAlias = false

	// No Agent fingerprint: only the native identity sentence is added, the
	// rest is forwarded untouched, brand names included.
	plain := `{"messages":[{"role":"system","content":"Generate a short title. Read ~/.codex/config.toml if needed."},{"role":"user","content":"Codex is great"}]}`
	r := intercept(config.Default(), "openai", plain)
	if r.Terminate || !strings.Contains(string(r.Body), `"You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team.\n\nGenerate a short title. Read ~/.codex/config.toml if needed."`) ||
		!strings.Contains(string(r.Body), "Codex is great") {
		t.Fatalf("plain request: %s %s", r.Body, r.ResponseBody)
	}
	for name, cfg := range map[string]config.Config{"legacy": legacy, "agent_alias off": aliasOff} {
		if r := intercept(cfg, "openai", plain); r.Terminate || strings.Contains(string(r.Body), "You are Antigravity") {
			t.Fatalf("%s added the identity or refused: %s %s", name, r.Body, r.ResponseBody)
		}
	}
	for _, tc := range []struct{ format, body, want string }{
		{"gemini", `{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`, `"systemInstruction":{"parts":[{"text":"You are Antigravity`},
		{"gemini", `{"contents":[],"system_instruction":{"parts":[]}}`, `"system_instruction":{"parts":[{"text":"You are Antigravity`},
		{"openai-response", `{"input":"hi"}`, `"instructions":"You are Antigravity`},
		{"openai", `{"messages":[{"role":"user","content":"hi"}]}`, `"messages":[{"content":"You are Antigravity`},
		{"claude", `{"messages":[{"role":"user","content":"hi"}]}`, `"system":"You are Antigravity`},
	} {
		r := intercept(config.Default(), tc.format, tc.body)
		if r.Terminate || !strings.Contains(string(r.Body), tc.want) {
			t.Fatalf("%s request without instructions: %s %s", tc.format, r.Body, r.ResponseBody)
		}
		if again := intercept(config.Default(), tc.format, string(r.Body)); again.Terminate || again.Body != nil {
			t.Fatalf("%s identity changed on retry: %s %s", tc.format, again.Body, again.ResponseBody)
		}
	}
}

func TestGuardConfig(t *testing.T) {
	for raw, want := range map[string][2]string{
		"": {"reject", "reject"},
		"brand_guard: off\nunidentified: legacy\n": {"off", "legacy"},
		"brand_guard: false\n":                     {"off", "reject"},
		"brand_guard: Strict\n":                    {"strict", "reject"},
		"brand_guard: report\n":                    {"report", "reject"},
	} {
		cfg, err := config.Parse([]byte(raw))
		if err != nil || string(cfg.BrandGuard) != want[0] || string(cfg.Unidentified) != want[1] {
			t.Fatalf("%q: %v %v %v", raw, cfg.BrandGuard, cfg.Unidentified, err)
		}
	}
	for _, raw := range []string{"brand_guard: true\n", "brand_guard: fallback\n", "unidentified: scrub\n", "unidentified: off\n"} {
		if _, err := config.Parse([]byte(raw)); err == nil {
			t.Fatalf("%q accepted", raw)
		}
	}
}

func TestUnidentifiedFingerprintResponse(t *testing.T) {
	var warnings []string
	p := New(func(method string, payload any) (json.RawMessage, error) {
		if method == "host.log" {
			if fields := payload.(map[string]any); fields["level"] == "warn" {
				warnings = append(warnings, fields["message"].(string))
			}
		}
		return providerResponse("agy"), nil
	})
	intercept := func(cfg config.Config, body string) pluginapi.RequestInterceptResponse {
		t.Helper()
		warnings = nil
		p.setConfig(cfg)
		var response pluginapi.RequestInterceptResponse
		callResult(t, p, pluginabi.MethodRequestInterceptAfter, pluginapi.RequestInterceptRequest{
			RequestID: "fingerprint", SourceFormat: "claude", Metadata: map[string]any{"selected_auth_index": "test"}, Body: []byte(body),
		}, &response)
		return response
	}
	tool := `{"name":"exec_command","description":"Run.","input_schema":{"type":"object","properties":{"cmd":{"type":"string"},"yield_time_ms":{"type":"number"}}}}`
	codex := `{"system":"Help the user.\n# AGENTS.md spec\nFollow the files.","tools":[` + tool + `],"messages":[{"role":"user","content":"hi"}]}`
	r := intercept(config.Default(), codex)
	if !r.Terminate || r.StatusCode != 400 || !strings.Contains(string(r.ResponseBody), `"code":"agent_bridge_unidentified"`) ||
		!strings.Contains(string(r.ResponseBody), `"family":"Codex"`) || !strings.Contains(string(r.ResponseBody), "codex/exec-command-yield") {
		t.Fatalf("Codex fingerprint forwarded: %+v %s", r, r.ResponseBody)
	}
	legacy := config.Default()
	legacy.Unidentified = config.UnidentifiedLegacy
	if r := intercept(legacy, codex); r.Terminate {
		t.Fatalf("legacy refused: %s", r.ResponseBody)
	}

	partial := `{"system":"Help the user.","tools":[` + tool + `],"messages":[{"role":"user","content":"hi"}]}`
	r = intercept(config.Default(), partial)
	if r.Terminate || !strings.Contains(string(r.Body), "You are Antigravity") || len(warnings) != 1 || !strings.Contains(warnings[0], "partial agent fingerprint") {
		t.Fatalf("partial fingerprint: %s %v", r.Body, warnings)
	}
}
