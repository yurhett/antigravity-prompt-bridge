package rewrite

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

func TestAgentPromptTemplatesMatchVerifiedSources(t *testing.T) {
	_, identities := identityConfig(t)
	raw, err := os.ReadFile("../../testdata/agent-tool-sources.json")
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
			if !changed || got != want || rewriter.stats.TemplateHits != 1 {
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
	result, err := rewriteAgentRequest(raw, config.Default())
	if err != nil || result.State == nil || !reflect.DeepEqual(result.Families, []string{"Pi"}) {
		t.Fatalf("wrong request profile: %+v %v", result, err)
	}
	root, _ := protocol.DecodeJSON(result.Body)
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
	if tools[0].(map[string]any)["description"] != "Read with antigravity. Call `antigravity_read`." || restoreName(t, result.State, "antigravity_read") != "pi_read" {
		t.Fatal(tools[0])
	}
	if result.Stats.TemplateHits != 1 || result.Stats.ResidualFixed != 3 || result.Stats.ResidualProtected < 4 {
		t.Fatalf("unexpected rewrite counts: %+v", result.Stats)
	}
	other := runProfileAlias(t, profileRequest("You are OpenCode. pi agent and PI agent. Run opencode run --model OpenCode. Use `opencode run`. Unlike Codex, OpenCode supports these tools.", profileTool("pi_read", "pi agent")))
	if other.system != nativeAntigravityIdentity+" pi agent and PI agent. Run opencode run --model OpenCode. Use `opencode run`. Unlike Codex, Antigravity supports these tools." || other.tools[0] != "pi_read" {
		t.Fatal(other)
	}
}

func TestQuotedAgentIdentityIsIgnored(t *testing.T) {
	for _, text := range []string{"`You are Pi.`", "``You are Pi.``", "```text\nYou are Pi.\n```", "> You are Pi.", "Example: You are Pi.", "Example:\nYou are Pi.", "For example, You are Pi.", "You are Pi.md."} {
		if profile := defaultBrandCatalog.identify([]string{text}); profile != nil {
			t.Fatalf("false identity match in %q", text)
		}
	}
}
