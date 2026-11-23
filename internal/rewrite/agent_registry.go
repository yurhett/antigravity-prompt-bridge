package rewrite

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"

	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

//go:embed assets/agent-adapter-traces.json
var agentAdapterTraceJSON []byte

type agentTraceExcerpt struct {
	Path string
	Text string
}

type agentTraceEvidence struct {
	Version, Source, Protocol, Scenario string
	RequestSHA256                       string `json:"request_sha256"`
	Fragments                           map[string]agentTraceExcerpt
	Tool                                *struct {
		Path, Name, Description string
		Schema                  map[string]any
	}
}

var agentTraceSamples = func() map[string]agentTraceEvidence {
	var samples map[string]agentTraceEvidence
	if err := json.Unmarshal(agentAdapterTraceJSON, &samples); err != nil {
		panic(err)
	}
	return samples
}()

func traceFragment(sample, name string) string {
	text := agentTraceSamples[sample].Fragments[name].Text
	if text == "" {
		panic("missing Agent trace fragment: " + sample + "/" + name)
	}
	return text
}

var geminiHelperAnchor = func() string {
	sentence, _, _ := strings.Cut(traceFragment("gemini", "cli_help"), ". ")
	return sentence + "."
}()

type agentRegistration struct {
	family      string
	recognize   func(protocol.AgentRequestView) bool
	templates   []agentPromptTemplate
	systemEdits func(string, promptProtection) ([]promptEdit, []textRange)
	execution   AgentAdapter
}

var registeredAgentAdapters = func() []agentRegistration {
	families := make(map[string]*agentRegistration)
	for _, brand := range agentBrands {
		family := brand.family()
		if families[family] == nil {
			families[family] = &agentRegistration{family: family}
		}
	}
	for _, template := range agentPromptTemplates {
		entry := families[template.Family]
		entry.templates = append(entry.templates, template)
	}
	addTraceTemplate := func(family, lane, fragment, replacement string, traceOnly bool) {
		template := agentPromptTemplate{
			Family: family, Match: traceFragment(lane, fragment), Replacement: replacement,
			Source: "internal/rewrite/assets/agent-adapter-traces.json", SourceSHA256: agentTraceSamples[lane].RequestSHA256,
			traceOnly: traceOnly,
		}
		template.compile()
		entry := families[family]
		entry.templates = append(entry.templates, template)
	}
	claude := families["Claude Code"]
	claude.recognize = recognizeClaudeTrace
	claude.systemEdits = claudeTraceSystemEdits
	addTraceTemplate(claude.family, "claude", "identity", nativeAntigravityIdentity, true)

	gemini := families["Gemini CLI"]
	gemini.recognize = recognizeGeminiTrace
	gemini.systemEdits = geminiTraceSystemEdits
	addTraceTemplate(gemini.family, "gemini", "identity", nativeAntigravityIdentity+" "+traceFragment("gemini", "identity"), true)
	gemini.templates[len(gemini.templates)-1].requiredLine = geminiHelperAnchor

	// The sentence only disambiguates two products named Codex; once renamed it
	// would describe a nonexistent model, so it is removed with its evidence.
	addTraceTemplate(families["Codex"].family, "codex", "disambiguation", "", false)

	openCode := families["OpenCode"]
	openCode.execution = openCodeAdapter{}
	addTraceTemplate(openCode.family, "opencode-v1", "identity_prefix", nativeAntigravityIdentity+" Please", false)
	addTraceTemplate(openCode.family, "opencode-v2", "identity", nativeAntigravityIdentity, false)

	registrations := make([]agentRegistration, 0, len(families))
	for _, registration := range families {
		sort.SliceStable(registration.templates, func(i, j int) bool {
			return len(registration.templates[i].Match) > len(registration.templates[j].Match)
		})
		registrations = append(registrations, *registration)
	}
	sort.Slice(registrations, func(i, j int) bool { return registrations[i].family < registrations[j].family })
	return registrations
}()

func identifyAgent(view protocol.AgentRequestView) *agentProfile {
	profile := defaultBrandCatalog.identify(view.System)
	for _, registration := range registeredAgentAdapters {
		if registration.recognize == nil || !registration.recognize(view) {
			continue
		}
		if profile == nil {
			profile = &agentProfile{catalog: defaultBrandCatalog, families: map[string]bool{}}
		}
		profile.families[registration.family] = true
		if profile.traceMatches == nil {
			profile.traceMatches = make(map[string]bool)
		}
		profile.traceMatches[registration.family] = true
	}
	return profile
}

func recognizeClaudeTrace(view protocol.AgentRequestView) bool {
	identity := traceFragment("claude", "identity")
	for _, text := range view.System {
		if strings.Contains(text, identity) && hasTracePromptLine(text, identity) {
			return true
		}
	}
	return false
}

func recognizeGeminiTrace(view protocol.AgentRequestView) bool {
	validDeclaration := false
	for _, tool := range view.Tools {
		if tool.Name == "invoke_agent" && hasStringParameter(tool, "agent_name") {
			validDeclaration = true
			break
		}
	}
	if !validDeclaration {
		return false
	}
	identity := traceFragment("gemini", "identity")
	for _, text := range view.System {
		if !strings.Contains(text, identity) || !strings.Contains(text, geminiHelperAnchor) {
			continue
		}
		if hasTracePromptLine(text, identity) && hasTracePromptLine(text, geminiHelperAnchor) {
			return true
		}
	}
	return false
}

func hasStringParameter(d protocol.AgentToolDeclaration, key string) bool {
	properties, _ := d.Schema["properties"].(map[string]any)
	property, _ := properties[key].(map[string]any)
	schemaType, _ := d.Schema["type"].(string)
	propertyType, _ := property["type"].(string)
	return strings.EqualFold(schemaType, "object") && strings.EqualFold(propertyType, "string")
}

// Trace evidence starts at a line boundary in the actual system text.
func hasTracePromptLine(text, fragment string) bool {
	protected := agentPromptProtection(text).all()
	for _, line := range tracePromptLines(text) {
		trimmed := strings.TrimLeft(line.text, " \t")
		start := line.start + len(line.text) - len(trimmed)
		tail, found := strings.CutPrefix(trimmed, fragment)
		if found && (tail == "" || strings.ContainsRune(" \t", rune(tail[0]))) &&
			!overlapsProtected(start, start+len(fragment), protected) {
			return true
		}
	}
	return false
}

type tracePromptLine struct {
	text       string
	start, end int
}

func tracePromptLines(text string) []tracePromptLine {
	var lines []tracePromptLine
	offset := 0
	for line := range strings.SplitAfterSeq(text, "\n") {
		if line == "" {
			break
		}
		lines = append(lines, tracePromptLine{strings.TrimRight(line, "\r\n"), offset, offset + len(line)})
		offset += len(line)
	}
	return lines
}
