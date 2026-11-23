package rewrite

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

// Request applies the recognized Agent rules or the legacy fallback.
func Request(body []byte, cfg config.Config) (RequestResult, error) {
	var result RequestResult
	if cfg.AgentAlias {
		var err error
		result, err = rewriteAgentRequest(body, cfg)
		if err != nil || len(result.Families) > 0 {
			return result, err
		}
	}
	result.Body, _ = rewriteRequestBodyWithConfig(body, cfg)
	return result, nil
}

type RequestResult struct {
	Body     []byte
	State    *AliasState
	Families []string
	Stats    PromptStats
}

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
	plans, err := buildAgentAdapterPlans(view, profile)
	if err != nil {
		return result, err
	}
	functions, namespaces := make(map[string]string), make(map[string]string)
	requiresRestore := len(plans) > 0
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
			return result, fmt.Errorf("native namespace alias %q is ambiguous", alias)
		}
		namespaces[alias] = ns
		if alias != ns {
			requiresRestore = true
		}
	}
	for _, slot := range slots {
		if slot.NamespaceOnly {
			continue
		}
		name := slot.Name()
		alias := aliasName(name)
		ns := aliasName(slot.Namespace)
		key := aliasToolKey(ns, alias)
		if old, ok := functions[key]; ok && old != name {
			return result, fmt.Errorf("tool name alias %q is ambiguous", alias)
		}
		functions[key] = name
		if alias != name {
			requiresRestore = true
		}
	}
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
		profile: profile, aliases: aliases, custom: config.NormalizeMappings(cfg.CustomMappings),
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
