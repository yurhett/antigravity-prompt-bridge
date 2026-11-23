package rewrite

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

func profileTool(name, desc string) map[string]any {
	return map[string]any{"name": name, "description": desc, "input_schema": map[string]any{"type": "object"}}
}

func profileRequest(system string, tools ...map[string]any) map[string]any {
	list := make([]any, 0, len(tools))
	for _, t := range tools {
		list = append(list, t)
	}
	return map[string]any{"system": system, "tools": list, "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
}

type profileResult struct {
	system string
	tools  []string
	state  *AliasState
}

func runProfileAlias(t *testing.T, root map[string]any) profileResult {
	t.Helper()
	raw, _ := json.Marshal(root)
	body, state, err := aliasAgentRequest(raw)
	if err != nil {
		t.Fatalf("alias error: %v", err)
	}
	if body == nil {
		body = raw
	}
	got, _ := protocol.DecodeJSON(body)
	object := got.(map[string]any)
	result := profileResult{system: object["system"].(string), state: state}
	for _, v := range object["tools"].([]any) {
		result.tools = append(result.tools, v.(map[string]any)["name"].(string))
	}
	return result
}

func restoreName(t *testing.T, state *AliasState, upstream string) string {
	t.Helper()
	block := map[string]any{"type": "tool_use", "id": "x", "name": upstream, "input": map[string]any{}}
	if err := state.restoreResponse(map[string]any{"content": []any{block}}); err != nil {
		t.Fatalf("restore %q: %v", upstream, err)
	}
	return block["name"].(string)
}

func TestProfileClaudeCodeToolsAndPrompt(t *testing.T) {
	r := runProfileAlias(t, profileRequest(
		"You are Claude Code, Anthropic's official CLI for Claude. Use the Read tool before Edit. Settings live in ~/.claude/settings.json; docs at https://docs.claude.com/claude-code. Claude Code supports hooks. Move the cursor to the end of the line. Call claude_code_status first.",
		profileTool("Read", "Read a file."), profileTool("Bash", "Run a shell command."), profileTool("claude_code_status", "Show status.")))
	if strings.Join(r.tools, ",") != "Read,Bash,antigravity_status" {
		t.Fatalf("tools: %v", r.tools)
	}
	if !strings.HasPrefix(r.system, nativeAntigravityIdentity+" Use the Read tool") || strings.Contains(r.system, "Anthropic's official CLI") {
		t.Fatalf("identity sentence not normalized: %q", r.system)
	}
	for _, keep := range []string{"~/.claude/settings.json", "https://docs.claude.com/claude-code", "Move the cursor", "Call antigravity_status first"} {
		if !strings.Contains(r.system, keep) {
			t.Fatalf("missing %q in %q", keep, r.system)
		}
	}
	if strings.Contains(strings.ReplaceAll(r.system, "docs.claude.com/claude-code", ""), "Claude Code") {
		t.Fatalf("brand left in prompt: %q", r.system)
	}
	for upstream, client := range map[string]string{"Read": "Read", "Bash": "Bash", "antigravity_status": "claude_code_status", "claude_code_status": "claude_code_status"} {
		if got := restoreName(t, r.state, upstream); got != client {
			t.Fatalf("restore %q = %q, want %q", upstream, got, client)
		}
	}
}

func TestProfileUnidentifiedRequestIsUntouched(t *testing.T) {
	for _, system := range []string{
		"You are a helpful database assistant. Use cascade_delete carefully; a cascade removes child rows.",
		"You are a helpful assistant. Tools follow OpenCode conventions; prefer opencode_read.",
		"Mention Claude Code and Codex only as references.",
	} {
		root := profileRequest(system, profileTool("cascade_delete", "Delete."), profileTool("opencode_read", "Read."), profileTool("cursor_next", "Advance."))
		raw, _ := json.Marshal(root)
		body, state, err := aliasAgentRequest(raw)
		if err != nil || body != nil || state != nil {
			t.Fatalf("unidentified request changed: %q err=%v body=%s", system, err, body)
		}
	}
}

func TestProfileAmbiguousOwnBrandMatchesWholeWords(t *testing.T) {
	r := runProfileAlias(t, profileRequest(
		"You are Cascade, a powerful agentic AI coding assistant. Use cascade_delete carefully; a cascade removes child rows. Cascade can open files. Move the cursor.",
		profileTool("cascade_delete", "Delete."), profileTool("cursor_next", "Advance.")))
	if strings.Join(r.tools, ",") != "antigravity_delete,cursor_next" {
		t.Fatalf("tools: %v", r.tools)
	}
	for _, keep := range []string{"Use antigravity_delete carefully", "a cascade removes child rows", "Antigravity can open files", "Move the cursor"} {
		if !strings.Contains(r.system, keep) {
			t.Fatalf("missing %q in %q", keep, r.system)
		}
	}
	if got := restoreName(t, r.state, "antigravity_delete"); got != "cascade_delete" {
		t.Fatal(got)
	}
}

func TestProfileLowercaseSelfNames(t *testing.T) {
	r := runProfileAlias(t, profileRequest(
		"You are a general-purpose AI agent called goose, created by AAIF. goose can load extensions. A goose is a bird.",
		profileTool("execute", "Execute python code."), profileTool("goose_memory", "Store memory.")))
	if strings.Join(r.tools, ",") != "execute,antigravity_memory" {
		t.Fatalf("tools: %v", r.tools)
	}
	if !strings.Contains(r.system, "antigravity can load extensions") || !strings.HasPrefix(r.system, nativeAntigravityIdentity+" antigravity can") {
		t.Fatalf("goose self name kept: %q", r.system)
	}
	if got := restoreName(t, r.state, "execute"); got != "execute" {
		t.Fatal(got)
	}

	r = runProfileAlias(t, profileRequest(
		"You are a coding assistant running inside pi, a coding agent harness. pi supports the Anthropic API.",
		profileTool("pi_extension", "Load.")))
	if !strings.HasPrefix(r.system, nativeAntigravityIdentity) || !strings.Contains(r.system, "antigravity supports the Anthropic API") || strings.Join(r.tools, ",") != "antigravity_extension" {
		t.Fatalf("pi: %q %v", r.system, r.tools)
	}
}

func TestProfileVerifiedIdentitiesAreRecognized(t *testing.T) {
	for _, system := range []string{
		"You are an AI agent running in OpenCode, a coding agent harness.",
		"You are Codex, based on GPT-5. You are running as a coding agent in the Codex CLI on a user's computer.",
		"You are a coding agent running in the Codex CLI, a terminal-based coding assistant.",
		"You are GPT-5.1 running in the Codex CLI, a terminal-based coding assistant.",
		"You are Gemini CLI, an interactive CLI agent specializing in software engineering tasks.",
		"You are Cline, an AI coding agent.",
		"You are Roo, a highly skilled software engineer.",
		"You are a general-purpose AI agent called goose, created by AAIF (Agentic AI Foundation).",
		"You are a coding assistant running inside pi, a coding agent harness.",
		"You are Hermes Agent, built by Nous Research.",
		"You are a powerful agentic AI coding assistant. You operate exclusively in Cursor, the world's best IDE.",
		"你是 Qwen Code，一个编程助手。",
	} {
		if defaultBrandCatalog.identify([]string{system}) == nil {
			t.Fatalf("not identified: %q", system)
		}
	}
	for _, system := range []string{
		"Use the variable called cursor to page results.",
		"Move the cursor. Codex and OpenCode are other tools.",
		"The goose flew away. Compute pi to ten digits.",
	} {
		if p := defaultBrandCatalog.identify([]string{system}); p != nil {
			t.Fatalf("false identification %v: %q", p.families, system)
		}
	}
}

// Every catalog path that the rewritten system prompt advertises must be a
// path the plugin can restore. This holds even when the agent's own brand is
// marked ambiguous, which previously split catalog and restore decisions.
func TestProfileCodeModeCatalogMatchesRestore(t *testing.T) {
	brands := append([]agentBrand(nil), agentBrands...)
	for i := range brands {
		if brands[i].Name == "OpenCode" {
			brands[i].Ambiguous = true
		}
	}
	for name, catalog := range map[string]*brandCatalog{"default": defaultBrandCatalog, "ambiguous": compileBrandCatalog(brands)} {
		saved := defaultBrandCatalog
		defaultBrandCatalog = catalog
		func() {
			defer func() { defaultBrandCatalog = saved }()
			root := codeModeRoot()
			raw, _ := json.Marshal(root)
			body, state, err := aliasAgentRequest(raw)
			if err != nil || state == nil || len(state.adapters) != 1 {
				t.Fatalf("%s: %v", name, err)
			}
			got, _ := protocol.DecodeJSON(body)
			system := got.(map[string]any)["system"].(string)
			paths := regexp.MustCompile(`tools\.[A-Za-z_]+\.[A-Za-z_]+`).FindAllString(system, -1)
			if len(paths) == 0 || strings.Contains(system, "tools.opencode") {
				t.Fatalf("%s: catalog not aliased: %q", name, system)
			}
			for _, path := range paths {
				call := protocol.AgentToolCall{Object: map[string]any{"name": "execute", "args": map[string]any{"code": "return await " + path + "();"}}, ArgumentKey: "args"}
				if err := state.adapters[0].RestoreCall(call); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if code := call.Object["args"].(map[string]any)["code"].(string); !strings.Contains(code, "tools.opencode.") {
					t.Fatalf("%s: advertised %s restores to %s", name, path, code)
				}
			}
		}()
	}
}

func TestKeywordModeKeepsOrdinaryWords(t *testing.T) {
	cfg := config.Default()
	cfg.HeuristicIdentity = false
	raw, _ := json.Marshal(map[string]any{"system": "You are Claude Code, Anthropic's official CLI. The room is ready. A cascade of events."})
	body, changed := rewriteRequestBodyWithConfig(raw, cfg)
	if !changed {
		t.Fatal("expected legacy keyword rewrite")
	}
	for _, keep := range []string{"Anthropic's official CLI", "The room is ready", "A cascade of events"} {
		if !strings.Contains(string(body), keep) {
			t.Fatalf("missing %q in %s", keep, body)
		}
	}
}

func TestProfileSelfIntroductionUsesNativeIdentity(t *testing.T) {
	for system, want := range map[string]string{
		"You are Gemini CLI, an interactive CLI agent built by Google. Keep GEMINI.md.": nativeAntigravityIdentity + " Keep GEMINI.md.",
		"You are Kiro, an AI assistant. Kiro's specs stay.":                             nativeAntigravityIdentity + " Antigravity's specs stay.",
		"你是 Trae，字节跳动开发的 AI 编程助手。Trae 支持多文件编辑。":                                         nativeAntigravityIdentity + " Antigravity 支持多文件编辑。",
	} {
		if got := runProfileAlias(t, profileRequest(system)).system; got != want {
			t.Fatalf("%q:\n got %q\nwant %q", system, got, want)
		}
	}
}

func TestProfileAmbiguousNamesUsedAsCommonNouns(t *testing.T) {
	for system, want := range map[string]string{
		"You are Cursor, an AI code editor. Move the cursor here. The Cursor editor is fast.":                nativeAntigravityIdentity + " Move the cursor here. The Antigravity editor is fast.",
		"You are a general-purpose AI agent called goose. goose can load extensions. A goose is a bird.":     nativeAntigravityIdentity + " antigravity can load extensions. A goose is a bird.",
		"You are Cascade, a coding agent. The cascade of errors matters. Cascade can open files.":            nativeAntigravityIdentity + " The cascade of errors matters. Antigravity can open files.",
		"You are GitHub Copilot. Your AI copilot is here. Copilot can edit.":                                 nativeAntigravityIdentity + " Your AI copilot is here. Antigravity can edit.",
		"You are a coding assistant running inside pi, a coding agent harness. Call the tool with pi agent.": nativeAntigravityIdentity + " Call the tool with antigravity agent.",
	} {
		if got := runProfileAlias(t, profileRequest(system)).system; got != want {
			t.Fatalf("%q:\n got %q\nwant %q", system, got, want)
		}
	}
}

func TestProfileVendorAttributionOnly(t *testing.T) {
	for system, want := range map[string]string{
		"You are Claude Code, a coding agent. Claude Code is built by Anthropic. ClaudeCode supports hooks. Use the Anthropic API.": nativeAntigravityIdentity + " Antigravity is built by the Google Deepmind team. Antigravity supports hooks. Use the Anthropic API.",
		"You are Kiro, an AI assistant. Kiro was created by the AWS team. Use AWS credentials.":                                     nativeAntigravityIdentity + " Antigravity was created by the Google Deepmind team. Use AWS credentials.",
		"You are Cascade, a coding agent. Windsurf (formerly Codeium) is fast. Cascade is a product of Cognition.":                  nativeAntigravityIdentity + " Antigravity is fast. Antigravity is a product of the Google Deepmind team.",
		"You are GitHub Copilot. Copilot follows Microsoft content policies. Microsoft's official docs apply.":                      nativeAntigravityIdentity + " Antigravity follows Microsoft content policies. Microsoft's official docs apply.",
		"你是 Trae，一个 AI 编程助手。Trae 由字节跳动开发。字节跳动的规范如下。":                                                                                nativeAntigravityIdentity + " Antigravity 由 Google Deepmind 团队开发。字节跳动的规范如下。",
		"You are Claude Code, a coding agent. Install Anthropic's official CLI extension.":                                          nativeAntigravityIdentity + " Install the official CLI extension.",
		"You are Codex, a coding agent. Use `built by OpenAI` verbatim.":                                                            nativeAntigravityIdentity + " Use `built by OpenAI` verbatim.",
	} {
		if got := runProfileAlias(t, profileRequest(system)).system; got != want {
			t.Fatalf("%q:\n got %q\nwant %q", system, got, want)
		}
	}
}

// Excerpts below are verbatim from captured Codex 0.171.0 and OpenCode 2.3.2
// requests (assets/agent-adapter-traces.json lanes codex and opencode-v2).
func TestProfileRealTraceExcerpts(t *testing.T) {
	codex := "You are a coding agent running in the Codex CLI, a terminal-based coding assistant. Codex CLI is an open source project led by OpenAI. You are expected to be precise, safe, and helpful.\n\n" +
		traceFragment("codex", "disambiguation") + "\n\n## Skills\n- skill-creator: Create or update a Codex skill with appropriately scoped instructions."
	want := nativeAntigravityIdentity + " You are expected to be precise, safe, and helpful.\n\n## Skills\n- skill-creator: Create or update an Antigravity skill with appropriately scoped instructions."
	if got := runProfileAlias(t, profileRequest(codex)).system; got != want {
		t.Fatalf("codex:\n got %q\nwant %q", got, want)
	}

	openCode := "You are an AI agent running in OpenCode, a coding agent harness.\n<available_skills>\n  <skill>\n    <id>opencode</id>\n    <name>OpenCode</name>\n" +
		"    <description>Use this skill for any question about OpenCode itself, including how OpenCode works.</description>\n  </skill>\n" +
		"  <skill>\n    <name>customize-opencode</name>\n    <description>Use when creating or fixing opencode agents.</description>\n  </skill>\n</available_skills>"
	want = nativeAntigravityIdentity + "\n<available_skills>\n  <skill>\n    <id>opencode</id>\n    <name>OpenCode</name>\n" +
		"    <description>Use this skill for any question about Antigravity itself, including how Antigravity works.</description>\n  </skill>\n" +
		"  <skill>\n    <name>customize-opencode</name>\n    <description>Use when creating or fixing antigravity agents.</description>\n  </skill>\n</available_skills>"
	if got := runProfileAlias(t, profileRequest(openCode)).system; got != want {
		t.Fatalf("opencode:\n got %q\nwant %q", got, want)
	}

	// A real indented code block (after a blank line) stays protected.
	block := "You are OpenCode, a coding agent.\n\n    OpenCode prints this line\n\nA Codex note."
	if got := runProfileAlias(t, profileRequest(block)).system; !strings.Contains(got, "\n\n    OpenCode prints this line\n") {
		t.Fatalf("indented code changed: %q", got)
	}
}

func TestProfileIndefiniteArticleBeforeAlias(t *testing.T) {
	got := runProfileAlias(t, profileRequest("You are Codex, a coding agent. A Codex skill is a folder; install a  Codex skill. Pick a Codex-CLI flag.")).system
	want := nativeAntigravityIdentity + " An Antigravity skill is a folder; install an  Antigravity skill. Pick a Codex-CLI flag."
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}
