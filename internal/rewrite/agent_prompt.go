package rewrite

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
)

//go:embed assets/agent-prompt-templates.json
var agentPromptTemplatesJSON []byte

type agentPromptTemplate struct {
	Family, Match, Replacement, Source string
	SourceSHA256                       string `json:"source_sha256"`
	pattern                            *regexp.Regexp
	traceOnly                          bool
	requiredLine                       string
}

var agentPromptTemplates = func() []agentPromptTemplate {
	var templates []agentPromptTemplate
	if err := json.Unmarshal(agentPromptTemplatesJSON, &templates); err != nil {
		panic(err)
	}
	for i := range templates {
		templates[i].compile()
	}
	sort.SliceStable(templates, func(i, j int) bool { return len(templates[i].Match) > len(templates[j].Match) })
	return templates
}()

func (t *agentPromptTemplate) compile() {
	words := strings.Fields(t.Match)
	for i := range words {
		words[i] = regexp.QuoteMeta(words[i])
	}
	t.pattern = regexp.MustCompile(`(?i)` + strings.Join(words, `[[:space:]\p{Zs}]+`))
}

type promptEdit struct {
	start, end  int
	replacement string
}

func addPromptEdit(edits []promptEdit, start, end int, replacement string) []promptEdit {
	for _, edit := range edits {
		if start < edit.end && end > edit.start {
			return edits
		}
	}
	return append(edits, promptEdit{start, end, replacement})
}

func applyPromptEdits(text string, edits []promptEdit) string {
	if len(edits) == 0 {
		return text
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var out strings.Builder
	left := 0
	for _, edit := range edits {
		out.WriteString(text[left:edit.start])
		out.WriteString(edit.replacement)
		left = edit.end
	}
	out.WriteString(text[left:])
	return out.String()
}

var (
	plainPromptExample  = regexp.MustCompile(`(?im)(?:^[\t ]*(?:[-*][\t ]+)?(?:quoted[\t ]+)?(?:[a-z]+[\t ]+)?examples?[\t ]*:|\bfor example[,:])[\t ]*(?:\r?\n[^\n]*|[^\n]*)`)
	plainPromptCommand  = regexp.MustCompile(`(?m)(?:^[\t ]*(?:[$%][\t ]+|PS>\s+)[^\n]+|\b(?:opencode|codex|claude|goose|pi|hermes|gemini)[\t ]+(?:--[a-zA-Z][\w-]*|-[a-zA-Z]\b|run\b|resume\b|serve\b|exec\b|attach\b|session\b|config\b))`)
	toolReferenceToken  = regexp.MustCompile(`[\pL\pN\pM_$-]+(?:\.[\pL\pN\pM_$-]+|\["[^"\n]+"\]|\['[^'\n]+'\])*`)
	toolReferenceBefore = regexp.MustCompile("(?i)\\b(?:call|invoke|tool)[ \\t]+`?$")
	toolReferenceAfter  = regexp.MustCompile("(?i)^`?[ \\t]+tool\\b")
)

func agentPromptProtection(text string) promptProtection {
	p := classifyPromptProtection(text)
	for _, hit := range plainPromptExample.FindAllStringIndex(text, -1) {
		p.blocks = append(p.blocks, textRange{hit[0], hit[1]})
	}
	if commands := plainPromptCommand.FindAllStringIndex(text, -1); len(commands) > 0 {
		protected := p.all()
		for _, hit := range commands {
			start, end := hit[0], hit[1]
			if !overlapsProtected(start, start+1, protected) {
				p.literals = append(p.literals, textRange{start, bareCommandEnd(text, end)})
			}
		}
	}
	return p
}

func bareCommandEnd(text string, start int) int {
	for offset, char := range text[start:] {
		at := start + offset
		if char == '\n' || char == ';' || char == '\r' {
			return at
		}
		if char == '.' || char == '!' || char == '?' {
			next, _ := utf8.DecodeRuneInString(text[at+1:])
			if at+1 == len(text) || unicode.IsSpace(next) {
				return at
			}
		}
	}
	return len(text)
}

func (p promptProtection) allowsReference(text string, start, end int, qualified, call bool) bool {
	if overlapsProtected(start, end, p.blocks) {
		return false
	}
	for _, literal := range p.technical {
		if start < literal.end && end > literal.start && (literal.start < start || literal.end > end) {
			return false
		}
	}
	for _, literal := range p.literals {
		if start >= literal.end || end <= literal.start {
			continue
		}
		if qualified && start < literal.start && literal.start < end {
			// A quoted member belongs to the validated path. The last bracket
			// member followed by a call can also look like a Markdown link.
			if end > literal.end && text[literal.start-1] == '[' && text[literal.end] == ']' {
				continue
			}
			if call && text[end-1] == ']' && (strings.HasPrefix(text[literal.start:], `["`) || strings.HasPrefix(text[literal.start:], `['`)) {
				continue
			}
		}
		return false
	}
	for _, code := range p.code {
		if start >= code.end || end <= code.start {
			continue
		}
		if code.inline && text[start:end] == strings.TrimSpace(strings.Trim(text[code.start:code.end], "`")) {
			continue
		}
		if !code.inline && qualified || call {
			continue
		}
		return false
	}
	return true
}

func rewriteToolReferences(text string, aliases map[string]string, protected promptProtection) string {
	if len(aliases) == 0 {
		return text
	}
	var edits []promptEdit
	for _, hit := range toolReferenceToken.FindAllStringIndex(text, -1) {
		start, end := hit[0], hit[1]
		name := text[start:end]
		alias, ok := aliases[name]
		if !ok {
			continue
		}
		token, _ := brandPathToken(text, start, end)
		if protectedBrandReference(token) || isPluginPath(token) {
			continue
		}
		qualified := strings.ContainsAny(name, ".[")
		call := strings.HasPrefix(text[end:], "(")
		if !qualified && !call && !strings.ContainsAny(name, "_-") &&
			!toolReferenceBefore.MatchString(text[max(0, start-16):start]) && !toolReferenceAfter.MatchString(text[end:]) {
			continue
		}
		if protected.allowsReference(text, start, end, qualified, call) {
			edits = append(edits, promptEdit{start, end, alias})
		}
	}
	return applyPromptEdits(text, edits)
}

type PromptStats struct {
	TemplateHits, ResidualFixed, ResidualProtected int
}

type agentPromptRewriter struct {
	profile *agentProfile
	aliases map[string]string
	custom  []config.Mapping
	stats   PromptStats
}

func (r *agentPromptRewriter) rewrite(text string, system bool) (string, bool) {
	next := text
	if system {
		next = applyIdentityMappings(next, r.custom, func(value string) []textRange {
			return agentPromptProtection(value).all()
		})
	}
	protection, removals := r.promptPolicy(next, system)
	if withAliases := rewriteToolReferences(next, r.aliases, protection); withAliases != next {
		next = withAliases
		protection, removals = r.promptPolicy(next, system)
	}
	if len(removals) > 0 {
		next = applyPromptEdits(next, removals)
		r.stats.TemplateHits += len(removals)
		protection, _ = r.promptPolicy(next, system)
	}
	protected := protection.all()
	if templated := r.applyTemplates(next, protected, system); templated != next {
		next = templated
		protection, _ = r.promptPolicy(next, system)
		protected = protection.all()
	}
	if attributed, hits := r.profile.rewriteAttributions(next, protected); hits > 0 {
		next = attributed
		r.stats.TemplateHits += hits
		protection, _ = r.promptPolicy(next, system)
		protected = protection.all()
	}
	residual, protectedCount := r.residualEdits(next, protected)
	// An identified Agent's own self-introduction always becomes the native
	// Antigravity sentence; renaming only the brand would keep vendor framing.
	if len(residual) > 0 && len(next) <= maxHeuristicPromptBytes {
		normalized := normalizeIdentityWhere(next, protected, func(candidate string) bool {
			if !isIdentityDeclaration(candidate, true) && !introducesSelf(candidate) {
				return false
			}
			profile := r.profile.catalog.identify([]string{candidate})
			return profile != nil && r.profile.includes(profile)
		})
		if normalized != next {
			oldCount := len(residual)
			next = normalized
			protection, _ = r.promptPolicy(next, system)
			residual, protectedCount = r.residualEdits(next, protection.all())
			r.stats.ResidualFixed += max(0, oldCount-len(residual))
		}
	}
	r.stats.ResidualProtected += protectedCount
	r.stats.ResidualFixed += len(residual)
	next = applyPromptEdits(next, residual)
	return next, next != text
}

func (r *agentPromptRewriter) promptPolicy(text string, system bool) (promptProtection, []promptEdit) {
	protection := agentPromptProtection(text)
	var edits []promptEdit
	if system {
		// Match against markup and code protection before adding the
		// Agent-specific spans that protect intact and drifted vendor prose.
		base := protection
		for _, registration := range registeredAgentAdapters {
			if registration.systemEdits == nil || !r.profile.families[registration.family] {
				continue
			}
			remove, keep := registration.systemEdits(text, base)
			protection.blocks = append(protection.blocks, keep...)
			edits = append(edits, remove...)
		}
	}
	return protection, edits
}

func (r *agentPromptRewriter) applyTemplates(text string, protected []textRange, system bool) string {
	var edits []promptEdit
	for _, registration := range registeredAgentAdapters {
		if !r.profile.families[registration.family] {
			continue
		}
		for _, template := range registration.templates {
			if template.traceOnly && (!system || !r.profile.traceMatches[template.Family]) {
				continue
			}
			if template.requiredLine != "" && (!strings.Contains(text, template.requiredLine) || !hasTracePromptLine(text, template.requiredLine)) {
				continue
			}
			for _, hit := range template.pattern.FindAllStringIndex(text, -1) {
				start, end := hit[0], hit[1]
				left := strings.TrimRight(text[:start], " \t")
				if left != "" && !strings.ContainsAny(left[len(left)-1:], ".!?;:\n\r-") || overlapsProtected(start, end, protected) {
					continue
				}
				if end < len(text) {
					next, _ := utf8.DecodeRuneInString(text[end:])
					if !unicode.IsSpace(next) && !strings.ContainsRune(")]}", next) {
						continue
					}
				}
				if template.Replacement == "" {
					end += len(text[end:]) - len(strings.TrimLeft(text[end:], " \t"))
					// A sentence alone on its line takes its line break with it.
					if lineStart := strings.TrimRight(text[:start], " \t"); lineStart == "" || strings.HasSuffix(lineStart, "\n") {
						for _, sep := range []string{"\r\n", "\n"} {
							if strings.HasPrefix(text[end:], sep) {
								end += len(sep)
								// A removed paragraph also takes one of its surrounding blank lines.
								if (lineStart == "" || strings.HasSuffix(lineStart, "\n\n") || strings.HasSuffix(lineStart, "\n\r\n")) && strings.HasPrefix(text[end:], sep) {
									end += len(sep)
								}
								break
							}
						}
					}
				}
				edits = addPromptEdit(edits, start, end, template.Replacement)
			}
		}
	}
	r.stats.TemplateHits += len(edits)
	return applyPromptEdits(text, edits)
}

func (p *agentProfile) includes(other *agentProfile) bool {
	for family := range other.families {
		if !p.families[family] {
			return false
		}
	}
	return true
}

func (r *agentPromptRewriter) residualEdits(text string, protected []textRange) ([]promptEdit, int) {
	type residualCandidate struct {
		textRange
		ambiguous bool
	}
	var candidates []residualCandidate
	for _, pat := range r.profile.catalog.patterns {
		if !r.profile.families[pat.brand.family()] {
			continue
		}
		for _, hit := range pat.prose.FindAllStringIndex(text, -1) {
			if proseBrandBoundary(text, hit[0], hit[1]) {
				candidates = append(candidates, residualCandidate{textRange{hit[0], hit[1]}, pat.brand.Ambiguous})
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].start == candidates[j].start {
			return candidates[i].end > candidates[j].end
		}
		return candidates[i].start < candidates[j].start
	})
	var edits []promptEdit
	protectedCount, previousEnd := 0, 0
	for _, candidate := range candidates {
		start, end := candidate.start, candidate.end
		if start < previousEnd {
			continue
		}
		previousEnd = end
		if overlapsProtected(start, end, protected) || technicalBrandUse(text, start, end) ||
			candidate.ambiguous && commonNounUse(text, start, end) {
			protectedCount++
			continue
		}
		replacement := aliasCase(text[start:end])
		// "a Codex skill" becomes "an Antigravity skill", not "a Antigravity".
		base := max(previousArticleEnd(edits), start-8)
		if article := indefiniteArticle.FindStringSubmatchIndex(text[base:start]); article != nil {
			// Keep the article's case and the original spacing: "A " -> "An ".
			replacement = text[base+article[2]:base+article[3]] + "n" + text[base+article[4]:base+article[5]] + replacement
			start = base + article[2]
		}
		edits = append(edits, promptEdit{start, end, replacement})
	}
	return edits, protectedCount
}

var (
	commonNounDeterminer = regexp.MustCompile(`(?i)(?:^|[^\pL\pN_])(?:a|an|the|this|that|these|those|each|every|any|some|no|my|your|his|her|its|our|their)[ \t]+(?:([\pL\pN-]+)[ \t]+)?$`)
	commonNounLinkWords  = map[string]bool{"with": true, "to": true, "for": true, "in": true, "on": true, "by": true, "from": true, "and": true, "or": true, "as": true, "at": true, "via": true, "than": true, "is": true, "are": true, "was": true, "be": true, "can": true, "will": true}
	commonNounOf         = regexp.MustCompile(`^[ \t]+of\b`)
)

// commonNounUse reports a lowercase ambiguous name used as an ordinary noun:
// "move the cursor", "a goose is a bird", "your AI copilot", "cascade of errors". A capitalized
// name after a determiner ("the Cursor editor") is still the product.
func commonNounUse(text string, start, end int) bool {
	first, _ := utf8.DecodeRuneInString(text[start:])
	if unicode.IsUpper(first) {
		return false
	}
	// One modifier may sit between the determiner and the noun ("your AI
	// copilot"); a preposition or verb there means the name starts a new phrase.
	if m := commonNounDeterminer.FindStringSubmatch(text[max(0, start-32):start]); m != nil && !commonNounLinkWords[strings.ToLower(m[1])] {
		return true
	}
	return commonNounOf.MatchString(text[end:])
}

var indefiniteArticle = regexp.MustCompile(`(?:^|[^\pL\pN_])([aA])([ \t]+)$`)

func previousArticleEnd(edits []promptEdit) int {
	if len(edits) == 0 {
		return 0
	}
	return edits[len(edits)-1].end
}

func technicalBrandUse(text string, start, end int) bool {
	if start > 0 && strings.ContainsRune(`./\:@~-`, rune(text[start-1])) {
		return true
	}
	if end < len(text) && strings.ContainsRune(`/\@-(`, rune(text[end])) {
		return true
	}
	if end+1 < len(text) && (text[end] == '.' || text[end] == ':') {
		next, _ := utf8.DecodeRuneInString(text[end+1:])
		if isToolWordRune(next) || next == '_' {
			return true
		}
	}
	rest := strings.TrimLeft(text[end:], " \t")
	return strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, "==")
}
