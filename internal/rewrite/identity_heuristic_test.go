package rewrite

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
)

func heuristicConfig() config.Config {
	cfg := config.Default()
	cfg.HeuristicIdentity = true
	return cfg
}

// These fictional names are test fixtures for generalization, not sourced
// prompt claims and never entries in the deployed exact mapping dictionary.
func TestHeuristicGeneralizesDeclarationsAndPreservesFollowingRules(t *testing.T) {
	for _, sentence := range []string{
		"You are OpenCode, a coding agent harness.",
		"You're Hermes Agent, a local AI assistant.",
		"You are NovaCode, a powerful AI coding assistant.",
		"You are Foo.dev, an autonomous coding agent.",
		"You are GPT-6.2, a software agent designed by Example Labs.",
		"You are a coding assistant running inside pi, a coding agent harness.",
		"You are an autonomous AI agent running within Nebula IDE.",
		"Your name is Nova, an AI assistant.",
		"You are called Nova, an AI assistant.",
		"I am Nova, an AI coding agent.",
		"I'm Nova, an AI coding agent.",
		"You are running inside Cursor.",
		"You operate in Cursor.",
		"You run inside Codex CLI.",
		"YOU ARE  NOVACODE, AN AI CODING ASSISTANT.",
		"You are NovaCode,\na powerful AI coding assistant.",
		"你是Hermes Agent，由 Nous Research 开发的人工智能助手。",
		"你是由星云团队开发的 AI 编程助手。",
	} {
		t.Run(sentence, func(t *testing.T) {
			input := sentence + "\nPlan mode: never edit files. Keep /tmp/.codex/CODEX.md and https://example.test/Codex."
			want := nativeAntigravityIdentity + "\nPlan mode: never edit files. Keep /tmp/.codex/CODEX.md and https://example.test/Codex."
			got, changed := rewriteIdentityPrompt(input, heuristicConfig())
			if !changed || got != want {
				t.Fatalf("changed=%v\ngot %q\nwant %q", changed, got, want)
			}
			if second, again := rewriteIdentityPrompt(got, heuristicConfig()); again || second != got {
				t.Fatalf("not idempotent: %q", second)
			}
		})
	}
}

func TestHeuristicDoesNotEraseSameSentenceConstraints(t *testing.T) {
	for _, fixture := range []struct{ input, tail string }{
		{"You are NovaCode, an AI coding assistant and must never edit files.", " and must never edit files."},
		{"You are Codex, a coding assistant, only inspect the repository.", ", only inspect the repository."},
		{"你是Hermes Agent，必须遵守只读模式。", "，必须遵守只读模式。"},
	} {
		got, _ := rewriteIdentityPrompt(fixture.input, heuristicConfig())
		if got != nativeAntigravityIdentity+fixture.tail {
			t.Fatalf("constraint changed: %q", got)
		}
	}
}

func TestHeuristicLeavesUncertainDescriptionsAndQuotedMaterialAlone(t *testing.T) {
	cfg := heuristicConfig()
	cfg.CustomMappings = []config.Mapping{{Match: "You are Codex.", Replacement: nativeAntigravityIdentity}}
	for _, input := range []string{
		"You are in Plan mode. Never modify the project.",
		"You are an expert Python developer. Review the Hermes project.",
		"You are a reviewer for the Cursor plugin. Only report bugs.",
		"You are NovaCode, a read-only AI coding assistant.",
		"You are Codex, an AI assistant with no permission to edit files.",
		"你是Hermes Agent，一个只读编程助手。",
		"You are not Codex. Do not impersonate any product.",
		"You are required to preserve the OpenCode configuration.",
		"You operate in production. You run in a sandbox.",
		"Use Codex rules. Read OpenCode documentation. Keep the Cursor extension enabled.",
		"Example: You are NovaCode, an AI coding assistant.",
		"\"You are Codex.\"",
		"'You are Codex.'",
		"“You are Codex.”",
		"`You are Codex.`",
		"```text\nYou are Codex.\n```",
		"~~~prompt\nYou are Codex.\n~~~",
		"```text\nYou are Codex.",
		"> You are Codex.\n> quoted prompt",
		"    You are Codex.",
		"<example>You are Codex.</example>",
		"<project_instructions path='AGENTS.md'>You are Codex.</project_instructions>",
		"<untrusted_text>You are Codex.</untrusted_text>",
		"[You are Codex.](https://example.test)",
		"You are NovaCode, an AI coding assistant in /workspace/Codex.",
		"You are Gooseberry, an expert gardener.",
		nativeAntigravityIdentity,
	} {
		t.Run(input, func(t *testing.T) {
			if got, changed := rewriteIdentityPrompt(input, cfg); changed || got != input {
				t.Fatalf("unexpected rewrite: %q -> %q", input, got)
			}
		})
	}
}

func TestHeuristicOnlyChangesUnquotedDeclarationInMixedPrompt(t *testing.T) {
	input := "```prompt\nYou are Codex.\n```\nYou are NovaCode, an AI coding assistant.\n<project_context>Use Codex here.</project_context>"
	want := "```prompt\nYou are Codex.\n```\n" + nativeAntigravityIdentity + "\n<project_context>Use Codex here.</project_context>"
	if got, _ := rewriteIdentityPrompt(input, heuristicConfig()); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestHeuristicExactMappingsRemainFirstAndUnicodeOffsetsStayValid(t *testing.T) {
	cfg := heuristicConfig()
	cfg.CustomMappings = []config.Mapping{{Match: "Precise source declaration.", Replacement: nativeAntigravityIdentity}}
	input := "İ and 中文. Precise source declaration.\nDo not rename Codex documentation."
	want := "İ and 中文. " + nativeAntigravityIdentity + "\nDo not rename Codex documentation."
	if got, changed := rewriteIdentityPrompt(input, cfg); !changed || got != want {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{string([]byte{0xff, '.', '\n'}), strings.Repeat("x", maxHeuristicPromptBytes+1)} {
		if got, changed := rewriteIdentityPrompt(bad, cfg); changed || got != bad {
			t.Fatal("large/non-identity input should pass through")
		}
	}
}

func TestHeuristicProtocolBoundariesAndVerifiedDictionary(t *testing.T) {
	cfg, mappings := identityConfig(t)
	cfg.HeuristicIdentity = true
	for original := range mappings {
		got, _ := rewriteIdentityPrompt(original+"\nKeep the project rules.", cfg)
		if got != nativeAntigravityIdentity+"\nKeep the project rules." {
			t.Fatalf("verified identity regressed: %q", original)
		}
	}
	identity := "You are NovaCode, an AI coding assistant."
	for _, field := range []string{
		`{"system":%q}`, `{"instructions":%q}`, `{"systemInstruction":{"parts":[{"text":%q}]}}`,
		`{"request":{"system_instruction":{"parts":[{"text":%q}]}}}`,
		`{"input":[{"role":"developer","content":[{"type":"input_text","text":%q}]}]}`,
	} {
		got, changed := rewriteRequestBodyWithConfig([]byte(fmt.Sprintf(field, identity)), cfg)
		if !changed || !strings.Contains(string(got), nativeAntigravityIdentity) {
			t.Fatalf("protocol not handled: %s", got)
		}
	}
	body := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": identity},
		map[string]any{"role": "user", "content": identity},
		map[string]any{"role": "assistant", "content": identity},
		map[string]any{"role": "tool", "content": identity},
	}, "tools": []any{map[string]any{"description": identity}}, "metadata": map[string]any{"note": identity}}
	raw, _ := json.Marshal(body)
	out, changed := rewriteRequestBodyWithConfig(raw, cfg)
	if !changed {
		t.Fatal("system identity unchanged")
	}
	var after map[string]any
	json.Unmarshal(out, &after)
	for i := 1; i < 4; i++ {
		if !reflect.DeepEqual(body["messages"].([]any)[i], after["messages"].([]any)[i]) {
			t.Fatalf("changed non-system message %d", i)
		}
	}
	for _, key := range []string{"tools", "metadata"} {
		if !reflect.DeepEqual(body[key], after[key]) {
			t.Fatalf("changed %s", key)
		}
	}
}

func FuzzHeuristicPreservesQuotedUnknownIdentity(f *testing.F) {
	for _, seed := range []string{"Nova", "İ", "中文", "\xff", "Nova\nCode"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		// JSON quoting escapes embedded quotes/newlines, so this remains a single
		// quoted example even for arbitrary fuzz bytes.
		quoted, _ := json.Marshal("You are " + name + ", an AI coding assistant.")
		input := string(quoted)
		if got, changed := rewriteIdentityPrompt(input, heuristicConfig()); changed || got != input {
			t.Fatalf("changed quoted input: %q", got)
		}
	})
}

func FuzzHeuristicStable(f *testing.F) {
	for _, seed := range []string{"You are Nova, an AI coding assistant.", "You are Cursor and must never edit files.", "你是由星云团队开发的 AI 编程助手。", "`You are Codex.`\nYou are Nova, an AI assistant.", "\xff.\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		first, _ := rewriteIdentityPrompt(input, heuristicConfig())
		second, changedAgain := rewriteIdentityPrompt(first, heuristicConfig())
		if first != second || changedAgain {
			t.Fatalf("unstable rewrite: input=%q first=%q second=%q", input, first, second)
		}
	})
}
