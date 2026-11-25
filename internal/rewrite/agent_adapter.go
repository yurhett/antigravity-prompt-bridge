package rewrite

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

// Adapters operate on protocol-defined views. They never walk arbitrary task data.
// AdapterError identifies unsupported adapter input.
type AdapterError struct{ err error }

func (e *AdapterError) Error() string { return e.err.Error() }
func adapterPlanError(err error) error {
	if strings.Contains(err.Error(), "ambiguous") {
		return err
	}
	return &AdapterError{err}
}

// Layer two: an adapter handles one agent feature that the generic tool-name
// layer cannot see, such as code that calls tools from inside one tool. It is
// matched by the feature itself and only runs once layer one identified the
// agent; every alias it needs comes from that profile.
type AgentAdapter interface {
	ID() string
	// Declared reports that the request offers the adapter's feature (for
	// Code Mode, an `execute` tool taking a `code` string). A declared
	// feature must then Match; brand_guard counts a miss as a coverage gap.
	Declared(protocol.AgentRequestView) bool
	Match(protocol.AgentRequestView) bool
	BuildPlan(protocol.AgentRequestView, *agentProfile) (AgentAdapterPlan, error)
}
type AgentAdapterPlan interface {
	Claims() []string
	RewriteRequest(protocol.AgentRequestView) (bool, error)
	RestoreCall(protocol.AgentToolCall) error
	// SystemIdentifiers lists client -> upstream identifiers that the
	// instructions may reference, rewritten before the prose pass.
	SystemIdentifiers() [][2]string
	// SystemEdits rewrites adapter-owned spans of one system text with the
	// plan's own mapping, before any prose rule runs.
	SystemEdits(text string) []promptEdit
	// Coverage reports the plan's own rules once every text was rewritten.
	Coverage() []CoverageCheck
}

func buildAgentAdapterPlans(view protocol.AgentRequestView, profile *agentProfile) ([]AgentAdapterPlan, error) {
	var plans []AgentAdapterPlan
	owners := map[string]string{}
	for _, registration := range registeredAgentAdapters {
		adapter := registration.execution
		// An adapter belongs to one Agent: structure alone would let another
		// client's same-named `execute` tool activate it.
		if adapter == nil || !profile.families[registration.family] || !adapter.Match(view) {
			continue
		}
		p, err := adapter.BuildPlan(view, profile)
		if err != nil {
			return nil, adapterPlanError(err)
		}
		for _, claim := range p.Claims() {
			if owner, ok := owners[claim]; ok {
				return nil, fmt.Errorf("agent adapters %s and %s claim the same field", owner, adapter.ID())
			}
			owners[claim] = adapter.ID()
		}
		plans = append(plans, p)
	}
	return plans, nil
}

// Preserve the caller's representation unless the adapter changed its value.
func transformCallCode(call protocol.AgentToolCall, transform func(string) (string, error)) (bool, error) {
	value, exists := call.Object[call.ArgumentKey]
	if !exists {
		return false, nil
	}
	object, ok := value.(map[string]any)
	encoded := false
	if !ok {
		if raw, ok := value.(string); ok {
			parsed, err := protocol.DecodeJSON([]byte(raw))
			if err != nil {
				return false, fmt.Errorf("invalid code tool arguments")
			}
			object, ok = parsed.(map[string]any)
			encoded = true
			if !ok {
				return false, fmt.Errorf("invalid code tool argument object")
			}
		}
	}
	if object == nil {
		return false, fmt.Errorf("invalid code tool argument object")
	}
	code, ok := object["code"].(string)
	if !ok {
		return false, fmt.Errorf("code tool missing string code")
	}
	next, err := transform(code)
	if err != nil {
		return false, err
	}
	if next == code {
		return false, nil
	}
	object["code"] = next
	if encoded {
		raw, _ := json.Marshal(object)
		call.Object[call.ArgumentKey] = string(raw)
	}
	return true, nil
}
