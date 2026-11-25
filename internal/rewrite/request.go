package rewrite

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

// aliasToolSlots builds the reverse maps for renamed native tools and namespaces.
func aliasToolSlots(slots []protocol.ToolNameSlot, aliasName func(string) string) (functions, namespaces map[string]string, renamed bool, err error) {
	functions, namespaces = make(map[string]string), make(map[string]string)
	for _, slot := range slots {
		ns := slot.Namespace
		if slot.NamespaceOnly {
			ns = slot.Name()
		}
		if ns == "" {
			continue
		}
		alias := aliasName(ns)
		if old, ok := namespaces[alias]; ok && old != ns {
			return nil, nil, false, fmt.Errorf("native namespace alias %q is ambiguous", alias)
		}
		namespaces[alias] = ns
		renamed = renamed || alias != ns
	}
	for _, slot := range slots {
		if slot.NamespaceOnly {
			continue
		}
		name := slot.Name()
		alias := aliasName(name)
		key := aliasToolKey(aliasName(slot.Namespace), alias)
		if old, ok := functions[key]; ok && old != name {
			return nil, nil, false, fmt.Errorf("tool name alias %q is ambiguous", alias)
		}
		functions[key] = name
		renamed = renamed || alias != name
	}
	return functions, namespaces, renamed, nil
}

type RequestResult struct {
	Body     []byte
	State    *AliasState
	Families []string
	Stats    PromptStats
	// Coverage lists rules the identified Agent's request should have hit
	// but did not; brand_guard decides what they cost.
	Coverage []CoverageCheck
	// Reject, when set, means the request must not reach upstream.
	Reject *Rejection
	// Fingerprints lists partial Agent fingerprints of an unidentified request
	// that was forwarded; the plugin logs them.
	Fingerprints []FingerprintMatch
}

// rewriteAgentRequest runs the identified Agent's rules and counts coverage.
func rewriteAgentRequest(body []byte, cfg config.Config) (RequestResult, error) {
	var result RequestResult
	root, err := protocol.DecodeJSON(body)
	if err != nil {
		return result, nil
	}
	slots, declarations := protocol.CollectTools(root)
	view := protocol.InspectRequestWithTools(root, declarations)
	profile := identifyAgent(view)
	if profile == nil {
		return result, nil
	}
	for family := range profile.families {
		result.Families = append(result.Families, family)
	}
	sort.Strings(result.Families)
	coverage := newCoverageCounter()
	expectCoverage(coverage, view, profile)
	plans, err := buildAgentAdapterPlans(view, profile)
	if err != nil {
		return result, err
	}
	for _, registration := range registeredAgentAdapters {
		if adapter := registration.execution; adapter != nil && profile.families[registration.family] && adapter.Match(view) {
			coverage.hit(adapterRule(adapter.ID()), 1)
		}
	}
	changed := false
	names := make(map[string]string)
	aliasName := func(name string) string {
		alias, ok := names[name]
		if !ok && name != "" {
			alias = profile.aliasName(name)
			names[name] = alias
		}
		return alias
	}
	functions, namespaces, renamed, err := aliasToolSlots(slots, aliasName)
	if err != nil {
		return result, err
	}
	requiresRestore := len(plans) > 0 || renamed
	aliases := declaredToolReferences(declarations, aliasName)
	for _, plan := range plans {
		for _, pair := range plan.SystemIdentifiers() {
			if pair[0] != pair[1] {
				aliases[pair[0]] = pair[1]
			}
		}
	}
	for _, slot := range slots {
		alias := aliasName(slot.Name())
		if alias != slot.Name() {
			slot.SetName(alias)
			changed = true
		}
	}
	for _, plan := range plans {
		did, err := plan.RewriteRequest(view)
		if err != nil {
			return result, adapterPlanError(err)
		}
		changed = changed || did
	}
	rewriter := agentPromptRewriter{
		profile: profile, aliases: aliases, custom: config.NormalizeMappings(cfg.CustomMappings), coverage: coverage, plans: plans,
	}
	_, systemChanged := protocol.TransformPromptFields(root, func(text string) (string, bool) {
		return rewriter.rewrite(text, true)
	})
	changed = changed || systemChanged
	for _, declaration := range declarations {
		changed = protocol.TransformToolDocumentation(declaration.Object, func(text string) (string, bool) {
			return rewriter.rewrite(text, false)
		}) || changed
	}
	result.Stats = rewriter.stats
	for _, plan := range plans {
		for _, check := range plan.Coverage() {
			coverage.add(check)
		}
	}
	result.Coverage = coverage.misses()
	if !changed {
		return result, nil
	}
	if requiresRestore {
		result.State = &AliasState{
			functions: functions, namespaces: namespaces, adapters: plans, renamed: true,
			calls: make(map[string]*streamAliasCall),
		}
	}
	result.Body, err = json.Marshal(root)
	return result, err
}
