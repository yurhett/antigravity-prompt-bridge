package rewrite

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
)

func coverageRules(t *testing.T, body []byte) []string {
	t.Helper()
	result, err := Request(body, config.Default(), "")
	if err != nil {
		t.Fatal(err)
	}
	var rules []string
	for _, miss := range result.Coverage {
		rules = append(rules, miss.Rule)
	}
	return rules
}

func TestCoverageCleanForRecordedCodeModeRequest(t *testing.T) {
	for _, heading := range []string{"## Available tools\n\n", ""} {
		if rules := coverageRules(t, liveCodeModeRequest(heading)); len(rules) != 0 {
			t.Fatalf("heading %q: unexpected misses %v", heading, rules)
		}
	}
}

// Project text quoted into the prompt is not a rule, so it never fails a request.
func TestCoverageIgnoresPreservedPromptContent(t *testing.T) {
	system := traceFragment("opencode-v2", "identity") + "\nProject notes: run `opencode serve`, config in ~/.config/opencode/opencode.json.\n```\nopencode run\n```"
	raw, _ := json.Marshal(map[string]any{"system": system})
	if rules := coverageRules(t, raw); len(rules) != 0 {
		t.Fatalf("preserved content counted as a gap: %v", rules)
	}
}

func TestCoverageReportsIdentityTemplateDrift(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"system": "You are OpenCode, the open source coding agent. Follow the project rules."})
	if rules := coverageRules(t, raw); !reflect.DeepEqual(rules, []string{"identity-template:OpenCode"}) {
		t.Fatalf("misses = %v", rules)
	}
}

// The adapter's feature is declared (execute with a code string) but no
// catalog outside an example: the adapter cannot run, which is a gap.
func TestCoverageReportsUnmatchedAdapter(t *testing.T) {
	system := traceFragment("opencode-v2", "identity") + "\n```text\n- tools.opencode.worktree_list(): Promise<{}>\n```\n"
	raw, _ := json.Marshal(map[string]any{"system": system, "tools": []any{map[string]any{"functionDeclarations": []any{codeModeTool()}}}})
	if rules := coverageRules(t, raw); !reflect.DeepEqual(rules, []string{"adapter:opencode-code-mode"}) {
		t.Fatalf("misses = %v", rules)
	}
}

func TestCoverageReportsUnmappedGroupLabel(t *testing.T) {
	system := traceFragment("opencode-v2", "identity") + "\n- tools.opencode.worktree_list(): Promise<{}>\n" +
		"- other (1 tool)\n  - tools.other.read(): Promise<{}>\n" +
		"```\n- opencode (1 tool)\n  - tools.opencode.example(): Promise<{}>\n```\n"
	raw, _ := json.Marshal(map[string]any{"system": system, "tools": []any{map[string]any{"functionDeclarations": []any{codeModeTool()}}}})
	result, err := Request(raw, config.Default(), "")
	if err != nil || len(result.Coverage) != 0 {
		t.Fatalf("mapped labels reported: %v %v", result.Coverage, err)
	}
	if got := string(result.Body); !strings.Contains(got, "- opencode (1 tool)\\n  - tools.opencode.example()") {
		t.Fatalf("label inside an example was renamed: %s", got)
	}
	plan := &openCodePlan{namespaces: map[string]string{"opencode": "antigravity"}}
	plan.SystemEdits("- opencode (1 tool)\n  - tools.opencode.a(): x\n- extra (1 tool)\n  - tools.extra.b(): x\n")
	if got := plan.Coverage(); len(got) != 1 || got[0] != (CoverageCheck{Rule: codeModeLabelRule, Expected: 2, Hits: 1, Level: CoverageGap}) {
		t.Fatalf("label coverage = %v", got)
	}
}

func TestBrandGuardRefusesByLevel(t *testing.T) {
	drift := CoverageCheck{Rule: "identity-template:X", Expected: 1, Level: CoverageDrift}
	gap := CoverageCheck{Rule: "adapter:x", Expected: 1, Level: CoverageGap}
	for _, tc := range []struct {
		mode   config.GuardMode
		misses []CoverageCheck
		refuse bool
	}{
		{config.GuardOff, []CoverageCheck{gap}, false},
		{config.GuardReport, []CoverageCheck{gap}, false},
		{config.GuardReject, []CoverageCheck{drift}, false},
		{config.GuardReject, []CoverageCheck{drift, gap}, true},
		{config.GuardStrict, []CoverageCheck{drift}, true},
	} {
		cfg := config.Default()
		cfg.BrandGuard = tc.mode
		result := RequestResult{Families: []string{"X"}, Coverage: tc.misses}
		guardIdentified(cfg, &result)
		if (result.Reject != nil) != tc.refuse || tc.refuse && result.Reject.Code != RejectCoverageGap {
			t.Errorf("%s %v: reject = %+v", tc.mode, tc.misses, result.Reject)
		}
	}
}

// A declared Code Mode tool without a usable catalog is a gap: the default
// reject mode refuses it instead of sending tools.opencode.* upstream.
func TestDefaultGuardRefusesUnmatchedAdapter(t *testing.T) {
	system := traceFragment("opencode-v2", "identity") + "\n```text\n- tools.opencode.worktree_list(): Promise<{}>\n```\n"
	raw, _ := json.Marshal(map[string]any{"system": system, "tools": []any{map[string]any{"functionDeclarations": []any{codeModeTool()}}}})
	result, err := Request(raw, config.Default(), "")
	if err != nil || result.Reject == nil || len(result.Reject.Coverage) != 1 || result.Reject.Coverage[0].Rule != "adapter:opencode-code-mode" {
		t.Fatalf("unmatched adapter forwarded: %+v %v", result.Reject, err)
	}
}
