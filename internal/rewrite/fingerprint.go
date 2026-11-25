package rewrite

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

// Each Agent has two highly distinctive features taken from recorded
// requests. An unidentified request that carries both is that Agent with an
// unrecognized identity; it is refused instead of being sent half-rewritten.
//
//go:embed assets/agent-fingerprints.json
var agentFingerprintJSON []byte

type fingerprintFeature struct {
	ID string `json:"id"`
	// Kind "text": Text occurs in a system/developer instruction.
	// Kind "tool": a declared tool named Tool, with parameter Parameter when set.
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Parameter string `json:"parameter,omitempty"`
	// Source is the recorded request the feature was taken from; Why says
	// why no other Agent or helper request carries it.
	Source string `json:"source"`
	Why    string `json:"why"`
}

type agentFingerprint struct {
	Family   string               `json:"family"`
	Features []fingerprintFeature `json:"features"`
}

var agentFingerprints = func() []agentFingerprint {
	var fingerprints []agentFingerprint
	if err := json.Unmarshal(agentFingerprintJSON, &fingerprints); err != nil {
		panic(err)
	}
	for _, f := range fingerprints {
		if len(f.Features) != 2 {
			panic(fmt.Sprintf("Agent fingerprint %s needs exactly two features", f.Family))
		}
		for _, feature := range f.Features {
			if feature.ID == "" || feature.Kind == "text" && feature.Text == "" || feature.Kind == "tool" && feature.Tool == "" ||
				feature.Kind != "text" && feature.Kind != "tool" {
				panic(fmt.Sprintf("invalid Agent fingerprint feature %s/%s", f.Family, feature.ID))
			}
		}
	}
	return fingerprints
}()

// FingerprintMatch lists the features of one Agent found in a request.
type FingerprintMatch struct {
	Family   string   `json:"family"`
	Features []string `json:"features"`
	Complete bool     `json:"complete"`
}

func (f fingerprintFeature) matches(view protocol.AgentRequestView) bool {
	switch f.Kind {
	case "text":
		for _, text := range view.System {
			if strings.Contains(text, f.Text) {
				return true
			}
		}
	case "tool":
		for _, tool := range view.Tools {
			if tool.Name != f.Tool {
				continue
			}
			if f.Parameter == "" {
				return true
			}
			properties, _ := tool.Schema["properties"].(map[string]any)
			if _, ok := properties[f.Parameter]; ok {
				return true
			}
		}
	}
	return false
}

// matchFingerprints returns every Agent with at least one matching feature.
func matchFingerprints(view protocol.AgentRequestView) []FingerprintMatch {
	var out []FingerprintMatch
	for _, fingerprint := range agentFingerprints {
		var hits []string
		for _, feature := range fingerprint.Features {
			if feature.matches(view) {
				hits = append(hits, feature.ID)
			}
		}
		if len(hits) > 0 {
			out = append(out, FingerprintMatch{Family: fingerprint.Family, Features: hits, Complete: len(hits) == len(fingerprint.Features)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Family < out[j].Family })
	return out
}
