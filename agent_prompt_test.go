package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestAgentPromptTemplatesMatchVerifiedSources(t *testing.T) {
	_, identities := identityConfig(t)
	raw, err := os.ReadFile("testdata/agent-tool-sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var sources []struct {
		Agent, Excerpt string
		URL            string `json:"source_url"`
		SHA            string `json:"source_sha256"`
	}
	if err := json.Unmarshal(raw, &sources); err != nil {
		t.Fatal(err)
	}
	for _, template := range agentPromptTemplates {
		t.Run(template.Family+"/"+template.Match, func(t *testing.T) {
			foundSource := false
			if template.Source == "testdata/verified-identities.json" && template.Family == "Codex" {
				for identity := range identities {
					if strings.Contains(identity, template.Match) && strings.Contains(identity, "Codex") {
						foundSource = true
					}
				}
			} else {
				for _, source := range sources {
					if source.URL == template.Source && len(source.SHA) == 64 && source.SHA == template.SourceSHA256 &&
						strings.EqualFold(source.Agent, template.Family) && strings.Contains(source.Excerpt, template.Match) {
						foundSource = true
					}
				}
			}
			if !foundSource {
				t.Fatal("no matching source excerpt", template.Source)
			}
			rewriter := agentPromptRewriter{profile: &agentProfile{catalog: defaultBrandCatalog, families: map[string]bool{template.Family: true}}}
			variant := strings.Join(strings.Fields(strings.ToUpper(template.Match)), " \t")
			got, changed := rewriter.rewrite(variant+"  Never modify user files.", true)
			want := "Never modify user files."
			if template.Replacement != "" {
				want = template.Replacement + "  " + want
			}
			if !changed || got != want || rewriter.stats.templateHits != 1 {
				t.Fatalf("got %q with counts %+v, want %q", got, rewriter.stats, want)
			}
		})
	}
}

func TestPiPromptTemplatesAndWholeWordResiduals(t *testing.T) {
	system := "YOU ARE A CODING ASSISTANT RUNNING INSIDE  PI, A CODING AGENT HARNESS. Never modify files.\n" +
		"pi agent; PI agent; api pipeline PiAgent π pi_undeclared tools.pi.unknown; const pi = 3.14.\n" +
		"Call `pi_read` and pi_read. Keep ``pi agent`` and `pi --mode json`.\n" +
		"Use /tmp/pi and https://pi.dev. Compare Codex and OpenCode.\n" +
		"```js\npi_read(); const sample = \"pi_read\";\n```\n" +
		"```bash\npi_read; pi --mode text\n```\n" +
		"> You are Cursor.\nExample: You are Goose.\nFor example, You are Hermes."
	want := nativeAntigravityIdentity + " Never modify files.\n" +
		"antigravity agent; ANTIGRAVITY agent; api pipeline PiAgent π pi_undeclared tools.pi.unknown; const pi = 3.14.\n" +
		"Call `antigravity_read` and antigravity_read. Keep ``pi agent`` and `pi --mode json`.\n" +
		"Use /tmp/pi and https://pi.dev. Compare Codex and OpenCode.\n" +
		"```js\nantigravity_read(); const sample = \"pi_read\";\n```\n" +
		"```bash\npi_read; pi --mode text\n```\n" +
		"> You are Cursor.\nExample: You are Goose.\nFor example, You are Hermes."
	raw, _ := json.Marshal(profileRequest(system, profileTool("pi_read", "Read with pi. Call `pi_read`."), profileTool("pipeline", ""), profileTool("api_read", ""), profileTool("cursor_next", "")))
	result, err := rewriteAgentRequest(raw, defaultFilterConfig())
	if err != nil || result.state == nil || !reflect.DeepEqual(result.families, []string{"Pi"}) {
		t.Fatalf("wrong request profile: %+v %v", result, err)
	}
	root, _ := decodeAliasJSON(result.body)
	got := root.(map[string]any)
	if got["system"] != want {
		t.Fatalf("system:\n%s\nwant:\n%s", got["system"], want)
	}
	tools := got["tools"].([]any)
	for i, name := range []string{"antigravity_read", "pipeline", "api_read", "cursor_next"} {
		if tools[i].(map[string]any)["name"] != name {
			t.Fatalf("unexpected renamed tool: %v", tools[i])
		}
	}
	if tools[0].(map[string]any)["description"] != "Read with antigravity. Call `antigravity_read`." || restoreName(t, result.state, "antigravity_read") != "pi_read" {
		t.Fatal(tools[0])
	}
	if result.stats.templateHits != 1 || result.stats.residualFixed != 3 || result.stats.residualProtected < 4 {
		t.Fatalf("unexpected rewrite counts: %+v", result.stats)
	}
	other := runProfileAlias(t, profileRequest("You are OpenCode. pi agent and PI agent. Run opencode run --model OpenCode. Use `opencode run`. Unlike Codex, OpenCode supports these tools.", profileTool("pi_read", "pi agent")))
	if other.system != nativeAntigravityIdentity+" pi agent and PI agent. Run opencode run --model OpenCode. Use `opencode run`. Unlike Codex, Antigravity supports these tools." || other.tools[0] != "pi_read" {
		t.Fatal(other)
	}
}

func TestAgentPromptRuntimeFallbackAuditAndStatelessRewrite(t *testing.T) {
	defer restoreDefaultFilterConfig(t)
	old := hostCall
	defer func() { hostCall = old }()
	var logs []map[string]any
	provider := "agy"
	hostCall = func(method string, payload any) (json.RawMessage, error) {
		if method == "host.log" {
			logs = append(logs, payload.(map[string]any)["fields"].(map[string]any))
		}
		return json.Marshal(map[string]any{"auth": map[string]any{"provider": provider}})
	}
	intercept := func(body string) pluginapi.RequestInterceptResponse {
		t.Helper()
		request := pluginapi.RequestInterceptRequest{SourceFormat: "unused-format", Stream: true, Metadata: map[string]any{"selected_auth_index": "test"}, Body: []byte(body)}
		var response pluginapi.RequestInterceptResponse
		aliasCallResult(t, pluginabi.MethodRequestInterceptAfter, request, &response)
		if response.Terminate {
			t.Fatalf("request terminated: %s", response.ResponseBody)
		}
		return response
	}
	cfg := defaultFilterConfig()
	cfg.Audit = true
	cfg.HeuristicIdentity = true
	cfg.CustomMappings = []rewriteMapping{{Match: "local_marker", Replacement: "project notes"}}
	applyFilterConfig(cfg)

	// A quoted identity must not enable aliases. The old identity and custom
	// mapping logic still run for the unrecognized Nova declaration.
	legacy := intercept(`{"system":"Example: ` + "`You are OpenCode.`" + `\nYou are Nova, an AI assistant created by Nova Labs. Keep local_marker.","tools":[{"name":"opencode_probe"}]}`)
	if !strings.Contains(string(legacy.Body), nativeAntigravityIdentity+" Keep project notes.") || !strings.Contains(string(legacy.Body), `"name":"opencode_probe"`) || logs[0]["agent_families"] != nil {
		t.Fatalf("wrong legacy rewrite: %s (%v)", legacy.Body, logs)
	}
	for _, text := range []string{"`You are Pi.`", "``You are Pi.``", "```text\nYou are Pi.\n```", "> You are Pi.", "Example: You are Pi.", "Example:\nYou are Pi.", "For example, You are Pi.", "You are Pi.md."} {
		if profile := defaultBrandCatalog.identify([]string{text}); profile != nil {
			t.Fatalf("false identity match in %q", text)
		}
	}

	provider = "antigravity"
	stateless := intercept(`{"systemInstruction":{"parts":[{"text":"You are a coding assistant running inside pi, a coding agent harness. Pi agent reads local_marker. ` + "`pi --mode json`" + `"}]},"tools":[{"functionDeclarations":[{"name":"read","description":"pi can read files","parameters":{"properties":{"value":{"description":"pi number","default":9007199254740993}}}}]}],"contents":[{"role":"user","parts":[{"text":"do the task"}]}]}`)
	if !strings.Contains(string(stateless.Body), "Antigravity agent reads project notes.") || !strings.Contains(string(stateless.Body), "9007199254740993") || agentAliases.get("") != nil {
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
	applyFilterConfig(cfg)
	disabled := intercept(`{"system":"You are OpenCode. Use local_marker.","tools":[{"name":"opencode_probe","description":"OpenCode"}]}`)
	if !strings.Contains(string(disabled.Body), "You are Antigravity. Use project notes.") || !strings.Contains(string(disabled.Body), `"description":"OpenCode","name":"opencode_probe"`) {
		t.Fatalf("wrong agent_alias off behavior: %s", disabled.Body)
	}
}
