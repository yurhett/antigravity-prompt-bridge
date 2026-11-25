package rewrite

import (
	"encoding/json"
	"strings"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

// Rejection is returned to the client instead of calling upstream.
type Rejection struct {
	Code         string             `json:"code"`
	Message      string             `json:"message"`
	Coverage     []CoverageCheck    `json:"coverage,omitempty"`
	Fingerprints []FingerprintMatch `json:"fingerprints,omitempty"`
}

const (
	RejectCoverageGap  = "agent_bridge_coverage_gap"
	RejectUnidentified = "agent_bridge_unidentified"
)

// Request applies the identified Agent's rules under brand_guard. With
// agent_alias on, an unidentified request follows the unidentified policy;
// with it off, every request takes the legacy rewrite.
func Request(body []byte, cfg config.Config, sourceFormat string) (RequestResult, error) {
	if cfg.AgentAlias {
		result, err := rewriteAgentRequest(body, cfg)
		if err != nil {
			return result, err
		}
		if len(result.Families) > 0 {
			guardIdentified(cfg, &result)
			return result, nil
		}
		if cfg.Unidentified == config.UnidentifiedReject {
			return handleUnidentified(body, sourceFormat)
		}
	}
	var result RequestResult
	result.Body, _ = rewriteRequestBodyWithConfig(body, cfg)
	return result, nil
}

// guardIdentified refuses gaps under reject, and drift as well under strict.
// Off and report never refuse; the plugin logs misses for report and up.
func guardIdentified(cfg config.Config, result *RequestResult) {
	refuse := false
	for _, miss := range result.Coverage {
		refuse = refuse || cfg.BrandGuard == config.GuardStrict || cfg.BrandGuard == config.GuardReject && miss.Level == CoverageGap
	}
	if refuse {
		result.Reject = &Rejection{Code: RejectCoverageGap, Message: CoverageSummary(result.Coverage), Coverage: result.Coverage}
	}
}

// handleUnidentified refuses a request carrying both fingerprint features of
// a known Agent: that Agent changed its identity and its rules no longer
// apply. Anything else is not a recognized Agent; it gets only the native
// identity sentence, as every Antigravity request has, and no other rewrite.
func handleUnidentified(body []byte, sourceFormat string) (RequestResult, error) {
	var result RequestResult
	root, err := protocol.DecodeJSON(body)
	if err != nil {
		return result, nil
	}
	matches := matchFingerprints(protocol.InspectRequest(root))
	for _, match := range matches {
		if match.Complete {
			result.Reject = &Rejection{
				Code: RejectUnidentified,
				Message: "request carries the " + match.Family + " fingerprint (" + strings.Join(match.Features, ", ") +
					") but its identity was not recognized; update antigravity-prompt-bridge",
				Fingerprints: matches,
			}
			return result, nil
		}
	}
	result.Fingerprints = matches
	if protocol.EnsureSystemIdentity(root, nativeAntigravityIdentity, sourceFormat) {
		result.Body, err = json.Marshal(root)
	}
	return result, err
}
