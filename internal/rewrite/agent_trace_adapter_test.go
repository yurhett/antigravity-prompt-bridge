package rewrite

import (
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

var traceProtocols = []string{"chat", "responses", "anthropic", "gemini"}

const traceUserText = "User example: You are a Claude agent, built on Anthropic's Claude Agent SDK. Claude Code is available elsewhere."

func projectTraceRequest(format string, system []string, tools []protocol.AgentToolDeclaration) map[string]any {
	root := make(map[string]any)
	prompt := make([]any, 0, len(system)+1)
	for _, text := range system {
		switch format {
		case "chat":
			prompt = append(prompt, map[string]any{"role": "system", "content": text})
		case "responses":
			prompt = append(prompt, map[string]any{"role": "developer", "content": []any{map[string]any{"type": "input_text", "text": text}}})
		case "anthropic":
			prompt = append(prompt, map[string]any{"type": "text", "text": text})
		default:
			prompt = append(prompt, map[string]any{"text": text})
		}
	}
	user := map[string]any{"role": "user", "content": traceUserText}
	switch format {
	case "chat":
		root["messages"] = append(prompt, user)
	case "responses":
		root["input"] = append(prompt, user)
	case "anthropic":
		root["system"], root["messages"] = prompt, []any{user}
	case "gemini":
		root["systemInstruction"] = map[string]any{"parts": prompt}
		root["contents"] = []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": traceUserText}}}}
	}
	declarations := make([]any, 0, len(tools))
	for _, tool := range tools {
		declaration := map[string]any{"name": tool.Name, "description": tool.Description}
		switch format {
		case "chat":
			declaration["parameters"] = tool.Schema
			declaration = map[string]any{"type": "function", "function": declaration}
		case "responses":
			declaration["type"] = "function"
			declaration["parameters"] = tool.Schema
		case "anthropic":
			declaration["input_schema"] = tool.Schema
		case "gemini":
			declaration["parametersJsonSchema"] = tool.Schema
		}
		declarations = append(declarations, declaration)
	}
	if format == "gemini" {
		declarations = []any{map[string]any{"functionDeclarations": declarations}}
	}
	root["tools"] = declarations
	return root
}

func recordedTraceTool(lane string) protocol.AgentToolDeclaration {
	tool := agentTraceSamples[lane].Tool
	return protocol.AgentToolDeclaration{Name: tool.Name, Description: tool.Description, Schema: tool.Schema}
}

func TestAgentTraceEvidenceAndRegistration(t *testing.T) {
	for _, lane := range []string{"opencode-v1", "opencode-v2", "opencode-v2-code-mode", "pi", "codex", "gemini", "claude"} {
		sample := agentTraceSamples[lane]
		digest, err := hex.DecodeString(sample.RequestSHA256)
		if len(digest) != 32 || err != nil || sample.Version == "" || !strings.HasPrefix(sample.Source, "https://") ||
			sample.Protocol == "" || sample.Scenario == "" || len(sample.Fragments) == 0 {
			t.Fatalf("incomplete capture evidence for %s: %+v", lane, sample)
		}
		for name, fragment := range sample.Fragments {
			if fragment.Text == "" || !strings.HasPrefix(fragment.Path, "/") {
				t.Fatalf("%s %s lacks a source field", lane, name)
			}
		}
		if sample.Tool != nil && (sample.Tool.Name == "" || !strings.HasPrefix(sample.Tool.Path, "/tools/")) {
			t.Fatalf("%s has no tool declaration evidence", lane)
		}
	}

	families := map[string]bool{}
	for _, registration := range registeredAgentAdapters {
		if families[registration.family] {
			t.Fatalf("duplicate registration %s", registration.family)
		}
		families[registration.family] = true
	}
	for _, family := range []string{"Claude Code", "Gemini CLI", "OpenCode", "Pi", "Codex", "Goose"} {
		if !families[family] {
			t.Fatalf("no registration for %s", family)
		}
	}
}

func TestAgentTraceAcrossProtocols(t *testing.T) {
	geminiIntro := traceFragment("gemini", "identity")
	helper := traceFragment("gemini", "cli_help")
	help := traceFragment("claude", "help")
	environment := traceFragment("claude", "environment")
	claudeRule := "# Doing tasks\n - Keep the project rules.\n"
	claudeTail := "# Context management\nContinue the actual task."
	openCodeV1 := traceFragment("opencode-v1", "identity")
	openCodeV1Tail := strings.TrimPrefix(openCodeV1, traceFragment("opencode-v1", "identity_prefix"))
	for _, sample := range []struct {
		lane, family, probe string
		system, expected    []string
		extra               []protocol.AgentToolDeclaration
	}{
		{"opencode-v1", "OpenCode", "opencode_probe",
			[]string{openCodeV1},
			[]string{nativeAntigravityIdentity + " Please" + openCodeV1Tail}, nil},
		{"opencode-v2", "OpenCode", "opencode_probe",
			[]string{traceFragment("opencode-v2", "identity") + " Help the user follow the project rules."},
			[]string{nativeAntigravityIdentity + " Help the user follow the project rules."}, nil},
		{"pi", "Pi", "pi_probe",
			[]string{traceFragment("pi", "identity") + " Follow the project rules."},
			[]string{nativeAntigravityIdentity + " Follow the project rules."}, nil},
		{"codex", "Codex", "codex_probe",
			[]string{traceFragment("codex", "identity") + " Follow the project rules."},
			[]string{nativeAntigravityIdentity + " Follow the project rules."}, nil},
		{"gemini", "Gemini CLI", "gemini_cli_probe",
			[]string{geminiIntro + " Follow the project rules.\n" + helper},
			[]string{nativeAntigravityIdentity + " " + geminiIntro + " Follow the project rules.\n" + helper},
			[]protocol.AgentToolDeclaration{recordedTraceTool("gemini")}},
		{"claude", "Claude Code", "claude_code_probe",
			[]string{traceFragment("claude", "identity"), claudeRule + help + "\n\n" + environment + "\n\n" + claudeTail},
			[]string{nativeAntigravityIdentity, claudeRule + "\n" + claudeTail}, nil},
	} {
		for _, format := range traceProtocols {
			for _, wrapped := range []bool{false, true} {
				name := sample.lane + "/" + format
				if wrapped {
					name += "/request"
				}
				t.Run(name, func(t *testing.T) {
					const reference = "Call `antigravity_probe` for the fixture."
					system := append(append([]string(nil), sample.system...), "Call `"+sample.probe+"` for the fixture.")
					expected := append(append([]string(nil), sample.expected...), reference)
					schema := map[string]any{"type": "object", "properties": map[string]any{
						"path": map[string]any{"type": "string", "default": sample.probe},
					}, "required": []any{"path"}}
					tools := append([]protocol.AgentToolDeclaration{{Name: sample.probe, Description: "Use " + sample.probe + ".", Schema: schema}}, sample.extra...)
					root := projectTraceRequest(format, system, tools)
					if wrapped {
						root = map[string]any{"request": root}
					}
					body, err := json.Marshal(root)
					if err != nil {
						t.Fatal(err)
					}
					cfg := config.Default()
					cfg.HeuristicIdentity = true
					result, err := rewriteAgentRequest(body, cfg)
					if err != nil || !reflect.DeepEqual(result.Families, []string{sample.family}) || result.State == nil {
						t.Fatalf("family/state: %v %v", result.Families, err)
					}
					got, err := protocol.DecodeJSON(result.Body)
					if err != nil {
						t.Fatal(err)
					}
					view := protocol.InspectRequest(got)
					if !reflect.DeepEqual(view.System, expected) {
						t.Fatalf("system = %q, want %q", view.System, expected)
					}
					if !strings.Contains(string(result.Body), traceUserText) {
						t.Fatal("user text changed")
					}
					decl := view.Tools[0]
					if decl.Name != "antigravity_probe" || decl.Description != "Use antigravity_probe." || !reflect.DeepEqual(decl.Schema, schema) {
						t.Fatalf("wrong rewritten declaration: %+v", decl)
					}
					call := map[string]any{"name": decl.Name, "args": map[string]any{"path": sample.probe}}
					if err := result.State.restoreFunction(call); err != nil || call["name"] != sample.probe ||
						!reflect.DeepEqual(call["args"], map[string]any{"path": sample.probe}) {
						t.Fatalf("wrong native tool restoration: %+v, %v", call, err)
					}
				})
			}
		}
	}
}

func TestGeminiTraceRequiresSystemAndToolEvidence(t *testing.T) {
	intro := traceFragment("gemini", "identity")
	help := traceFragment("gemini", "cli_help")
	tool := recordedTraceTool("gemini")
	for _, variant := range []struct {
		name   string
		system []string
		tools  []protocol.AgentToolDeclaration
	}{
		{"no-intro", []string{help}, []protocol.AgentToolDeclaration{tool}},
		{"no-help", []string{intro}, []protocol.AgentToolDeclaration{tool}},
		{"no-declaration", []string{intro + "\n" + help}, nil},
		{"wrong-schema", []string{intro + "\n" + help}, []protocol.AgentToolDeclaration{{Name: tool.Name, Schema: map[string]any{"type": "object"}}}},
		{"split-text", []string{intro, help}, []protocol.AgentToolDeclaration{tool}},
		{"quoted-help", []string{intro + "\n> " + help}, []protocol.AgentToolDeclaration{tool}},
		{"fenced-intro", []string{"```text\n" + intro + "\n```\n" + help}, []protocol.AgentToolDeclaration{tool}},
		{"inline-example", []string{"Example:\n" + intro + "\n" + help}, []protocol.AgentToolDeclaration{tool}},
	} {
		t.Run(variant.name, func(t *testing.T) {
			root := projectTraceRequest("chat", variant.system, variant.tools)
			root["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "invoke_agent"}}
			messages := root["messages"].([]any)
			root["messages"] = append(messages, map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"function": map[string]any{"name": "invoke_agent", "arguments": `{"agent_name":"cli_help"}`}},
			}}, map[string]any{"role": "user", "content": intro + "\n" + help})
			raw, _ := json.Marshal(root)
			result, err := rewriteAgentRequest(raw, config.Default())
			if err != nil || result.Body != nil || len(result.Families) != 0 {
				t.Fatalf("incorrectly identified %s: %v %s %v", variant.name, result.Families, result.Body, err)
			}
		})
	}
	system := []string{intro + "\n" + help, intro + " Preserve this unrelated block."}
	raw, _ := json.Marshal(projectTraceRequest("chat", system, []protocol.AgentToolDeclaration{tool}))
	result, err := rewriteAgentRequest(raw, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	got, err := protocol.DecodeJSON(result.Body)
	if err != nil || result.State != nil || protocol.PromptTexts(got)[1] != system[1] {
		t.Fatalf("Gemini template applied outside its evidence block: %s %v", result.Body, err)
	}

	// Provider Schema and JSON Schema represent the same parameter types.
	uppercaseTypes := map[string]any{"type": "OBJECT", "properties": map[string]any{
		"agent_name": map[string]any{"type": "STRING"},
	}}
	tool.Schema = uppercaseTypes
	root := projectTraceRequest("gemini", system[:1], []protocol.AgentToolDeclaration{tool})
	decl := root["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	delete(decl, "parametersJsonSchema")
	decl["parameters"] = uppercaseTypes
	raw, _ = json.Marshal(root)
	result, err = rewriteAgentRequest(raw, config.Default())
	if err != nil || !reflect.DeepEqual(result.Families, []string{"Gemini CLI"}) {
		t.Fatalf("provider Schema did not match the same evidence: %s %v", result.Body, err)
	}
}

func TestClaudeTraceDeletionPreservesDriftAndExamples(t *testing.T) {
	identity := traceFragment("claude", "identity")
	help := traceFragment("claude", "help")
	lines := strings.Split(traceFragment("claude", "environment"), "\n")
	drift := strings.Replace(lines[3], "Claude Opus", "a compatible model", 1)
	added := " - Ask the user before changing Claude Code workspace settings."
	more := "  - Keep fixture feedback in the current project."
	example := "```text\n# Doing tasks\n" + help + "\n" + strings.Join(lines, "\n") + "\n```"
	before := "# Doing tasks\n" + help + "\n" + more + "\n\n" + strings.Join(lines[:3], "\n") +
		"\n" + drift + "\n" + added + "\n\n# Examples\n" + example
	want := "# Doing tasks\n" + help + "\n" + more + "\n\n# Environment\n" +
		drift + "\n" + added + "\n\n# Examples\n" + example
	raw, _ := json.Marshal(projectTraceRequest("anthropic", []string{identity, before}, nil))
	cfg := config.Default()
	cfg.HeuristicIdentity = true
	result, err := rewriteAgentRequest(raw, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := protocol.DecodeJSON(result.Body)
	if err != nil || !reflect.DeepEqual(protocol.PromptTexts(got), []string{nativeAntigravityIdentity, want}) {
		t.Fatalf("wrong Claude drift rewrite: %s %v", result.Body, err)
	}

	addedSection := "## Workspace\n" + added
	raw, _ = json.Marshal(projectTraceRequest("anthropic", []string{
		identity, strings.Join(lines, "\n") + "\n\n" + addedSection,
	}, nil))
	result, err = rewriteAgentRequest(raw, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err = protocol.DecodeJSON(result.Body)
	if err != nil || !reflect.DeepEqual(protocol.PromptTexts(got), []string{nativeAntigravityIdentity, "# Environment\n\n" + addedSection}) {
		t.Fatalf("deletion did not preserve the existing parent and new subsection: %s %v", result.Body, err)
	}
}
