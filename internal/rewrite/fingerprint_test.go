package rewrite

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

func TestFingerprintDataBelongsToRegisteredAgents(t *testing.T) {
	families := map[string]bool{}
	for _, registration := range registeredAgentAdapters {
		families[registration.family] = true
	}
	ids := map[string]bool{}
	for _, fingerprint := range agentFingerprints {
		if !families[fingerprint.Family] {
			t.Errorf("fingerprint for unknown family %q", fingerprint.Family)
		}
		for _, feature := range fingerprint.Features {
			if ids[feature.ID] || feature.Source == "" || feature.Why == "" {
				t.Errorf("feature %s: duplicate ID or missing provenance", feature.ID)
			}
			ids[feature.ID] = true
		}
	}
}

// fingerprintRequest declares the given features in a request with no
// recognizable identity, so only the fingerprint can tell the Agent.
func fingerprintRequest(features ...fingerprintFeature) []byte {
	system := "Help the user with their project."
	var tools []any
	for _, f := range features {
		if f.Kind == "text" {
			system += "\n" + f.Text
			continue
		}
		properties := map[string]any{}
		if f.Parameter != "" {
			properties[f.Parameter] = map[string]any{"type": "string"}
		}
		tools = append(tools, map[string]any{"name": f.Tool, "description": "Tool.", "input_schema": map[string]any{"type": "object", "properties": properties}})
	}
	raw, _ := json.Marshal(map[string]any{"system": system, "tools": tools, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	return raw
}

func TestFingerprintRefusesUnidentifiedKnownAgent(t *testing.T) {
	for _, fingerprint := range agentFingerprints {
		result, err := Request(fingerprintRequest(fingerprint.Features...), config.Default(), "claude")
		if err != nil || len(result.Families) != 0 || result.Reject == nil || result.Reject.Code != RejectUnidentified ||
			!strings.Contains(result.Reject.Message, fingerprint.Family) {
			t.Fatalf("%s: both features forwarded: %+v %v", fingerprint.Family, result.Reject, err)
		}

		// One feature alone is forwarded with the identity, and reported.
		result, err = Request(fingerprintRequest(fingerprint.Features[0]), config.Default(), "claude")
		if err != nil || result.Reject != nil || len(result.Fingerprints) != 1 || result.Fingerprints[0].Complete ||
			!strings.Contains(string(result.Body), nativeAntigravityIdentity) {
			t.Fatalf("%s: single feature: %+v %+v %v", fingerprint.Family, result.Reject, result.Fingerprints, err)
		}
	}
}

func TestFingerprintRequiresStructure(t *testing.T) {
	// A tool feature needs the named parameter, not just the tool name.
	f := fingerprintFeature{ID: "x", Kind: "tool", Tool: "exec_command", Parameter: "yield_time_ms"}
	view := protocol.AgentRequestView{Tools: []protocol.AgentToolDeclaration{{Name: "exec_command", Schema: map[string]any{"properties": map[string]any{"cmd": map[string]any{}}}}}}
	if f.matches(view) {
		t.Fatal("tool matched without its parameter")
	}
	// Text features only look at instructions, never at user content.
	raw, _ := json.Marshal(map[string]any{"system": "Summarize.", "messages": []any{map[string]any{"role": "user", "content": "# AGENTS.md spec\n# Core Mandates\n# Using your tools"}}})
	if result, _ := Request(raw, config.Default(), "claude"); result.Reject != nil || len(result.Fingerprints) != 0 {
		t.Fatalf("user content matched a fingerprint: %+v", result.Reject)
	}
}

// Identified requests are governed by brand_guard; fingerprints only apply
// when no Agent was identified.
func TestFingerprintIgnoredForIdentifiedAgent(t *testing.T) {
	var openCode []fingerprintFeature
	for _, fingerprint := range agentFingerprints {
		if fingerprint.Family == "OpenCode" {
			openCode = fingerprint.Features
		}
	}
	raw := fingerprintRequest(openCode...)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	body["system"] = traceFragment("opencode-v2", "identity") + "\n" + body["system"].(string)
	raw, _ = json.Marshal(body)
	if result, err := Request(raw, config.Default(), "claude"); err != nil || result.Reject != nil || len(result.Families) != 1 {
		t.Fatalf("identified request refused: %+v %v", result.Reject, err)
	}
}
