package main

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Observed verbatim in official agy conversation gen_metadata; not a synthetic persona.
const nativeAntigravityIdentity = "You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team."

const maxHeuristicPromptBytes = 256 * 1024
const maxIdentitySentenceBytes = 2048

func providerFilterEnabled(provider string, cfg filterConfig) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "agy":
		return cfg.EnableAgy
	case "antigravity":
		return cfg.EnableAntigravity
	default:
		return false
	}
}

var (
	identityIntro           = regexp.MustCompile(`(?i)^(?:you\s+are\s+called|you\s+are|you['’]re|i\s+am|i['’]m|your\s+name\s+is)\s+`)
	runtimeIntro            = regexp.MustCompile(`(?i)^you\s+(?:operate|run|work)\s+(?:in|inside|within|as)\s+`)
	runtimeDescription      = regexp.MustCompile(`(?i)\b(?:running|operating|working|embedded|integrated|hosted)\s+(?:in|inside|within|as)\b`)
	creatorDescription      = regexp.MustCompile(`(?i)\b(?:built|created|developed|designed|powered)\s+by\b`)
	agentDescription        = regexp.MustCompile(`(?i)\b(?:ai|coding|code|software|agentic|autonomous)(?:[- \t]+[a-z]+){0,5}[- \t]+(?:assistant|agent|harness)\b`)
	environmentDescription  = regexp.MustCompile(`(?i)\b(?:cli|ide|harness|terminal|editor)\b`)
	unknownLeadingName      = regexp.MustCompile(`^[\p{Lu}][\p{L}\p{N}_+.-]{1,48}(?:[ -](?:Agent|Code|CLI|Assistant)){0,2}\s*[,—(]`)
	behaviorBoundary        = regexp.MustCompile(`(?i)(?:,\s*|\s+(?:and|but)\s+|\s+)(?:you\s+)?(?:must|should|shall|never|always|do\s+not|don't|cannot|can't|only|help\s+(?:the\s+)?user|your\s+(?:task|goal)\s+is)\b`)
	chineseBehaviorBoundary = regexp.MustCompile(`[，,]?你?(?:必须|不能|不得|不要|请|只能|始终|应该)`)
	forbiddenIdentity       = regexp.MustCompile(`(?i)^(?:not\b|required\b|expected\b|allowed\b|forbidden\b|unable\b|in\b|to\b|responsible\b|working\s+on\b)`)
	embeddedConstraint      = regexp.MustCompile(`(?i)\b(?:read[- ]only|restricted|limited|without|no\s+(?:permission|write|editing)|not\s+allowed)\b|只读|不得|禁止`)
	inlineQuoteRanges       = regexp.MustCompile("\"(?:\\\\.|[^\"\\\\])*\"|“[^”]*”|‘[^’]*’|(?:^|[ \\t(])'[^'\\n]+'")
	markdownLinkRanges      = regexp.MustCompile(`\[[^\]\n]*\]\([^\)\n]*\)`)
	xmlProtectedStart       = regexp.MustCompile(`(?i)<(?:example|examples|quote|quoted|code|tool_result|tool_output|project_instructions|project_context|untrusted_text|document)\b[^>]*>`)
	// Single-line identifier elements in harness listings (<id>opencode</id>,
	// <name>customize-opencode</name>) are lookup keys, not prose.
	xmlIdentifierValue   = regexp.MustCompile(`(?i)<(?:id|name|location|path|file)>[^<\n]{0,256}</(?:id|name|location|path|file)>`)
	identityAliasPattern = buildIdentityAliasPattern()
)

func buildIdentityAliasPattern() *regexp.Regexp {
	names := []string{"Antigravity"}
	for _, brand := range agentBrands {
		names = append(names, brand.Name)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for i := range names {
		names[i] = regexp.QuoteMeta(names[i])
	}
	return regexp.MustCompile(`(?i)^(?:` + strings.Join(names, "|") + `)(?:$|[^\p{L}\p{N}_-])`)
}

// Exact custom mappings run first. The heuristic infers declarations, never new
// custom dictionary entries. In this mode the keyword preset supplies identity
// hints instead of replacing unrelated mentions inside rules and descriptions.
// No network/model calls or persisted prompt text.
func rewriteIdentityPrompt(value string, cfg filterConfig) (string, bool) {
	if len(value) > maxHeuristicPromptBytes {
		return value, false
	}
	next := applyIdentityMappings(value, normalizeMappings(cfg.CustomMappings), identityProtectedRanges)
	next = normalizeIdentityDeclarations(next, cfg.UseDefaultKeywords)
	return next, next != value
}

func applyIdentityMappings(value string, mappings []rewriteMapping, protection func(string) []textRange) string {
	for _, m := range mappings {
		// RE2 reports byte offsets in the original string even when Unicode case
		// folding changes byte lengths. Lowercased-string offsets are unsafe here.
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(m.Match))
		hits := re.FindAllStringIndex(value, -1)
		if len(hits) == 0 {
			continue
		}
		protected := protection(value)
		var b strings.Builder
		start := 0
		for _, hit := range hits {
			if overlapsProtected(hit[0], hit[1], protected) {
				continue
			}
			b.WriteString(value[start:hit[0]])
			b.WriteString(m.Replacement)
			start = hit[1]
		}
		if start > 0 {
			b.WriteString(value[start:])
			value = b.String()
		}
	}
	return value
}

func normalizeIdentityDeclarations(value string, useAliases bool) string {
	return normalizeIdentityWhere(value, identityProtectedRanges(value), func(candidate string) bool {
		return isIdentityDeclaration(candidate, useAliases)
	})
}

func normalizeIdentityWhere(value string, protected []textRange, matches func(string) bool) string {
	var b strings.Builder
	start, copied, changes := 0, 0, 0
	consume := func(end int) {
		if end-start > maxIdentitySentenceBytes || changes >= 128 {
			start = end
			return
		}
		segment := value[start:end]
		trimmed := strings.TrimSpace(segment)
		left := start + strings.Index(segment, trimmed)
		right := left + len(trimmed)
		if trimmed != "" && !overlapsProtected(left, right, protected) {
			// Preserve any same-sentence behavioral clause, rather than erasing
			// constraints just because a product name appeared before it.
			identityEnd := len(trimmed)
			if intro := identityIntro.FindStringIndex(trimmed); intro != nil {
				if tail := behaviorBoundary.FindStringIndex(trimmed[intro[1]:]); tail != nil {
					identityEnd = intro[1] + tail[0]
				}
			} else if strings.HasPrefix(trimmed, "你") {
				if tail := chineseBehaviorBoundary.FindStringIndex(trimmed); tail != nil {
					identityEnd = tail[0]
				}
			}
			candidate := strings.TrimSpace(trimmed[:identityEnd])
			if !strings.HasPrefix(candidate, nativeAntigravityIdentity) && matches(candidate) {
				b.WriteString(value[copied:left])
				b.WriteString(nativeAntigravityIdentity)
				copied = left + len(candidate)
				// CJK sentences have no space after "。"; keep the next word separate.
				if next, _ := utf8.DecodeRuneInString(value[copied:]); copied < len(value) && !unicode.IsSpace(next) && !unicode.IsPunct(next) {
					b.WriteByte(' ')
				}
				changes++
			}
		}
		start = end
	}
	for i, r := range value {
		_, n := utf8.DecodeRuneInString(value[i:])
		boundary := strings.ContainsRune("!?;。！？；", r)
		if r == '.' {
			prev, _ := utf8.DecodeLastRuneInString(value[:i])
			next, _ := utf8.DecodeRuneInString(value[i+n:])
			boundary = !(isNameRune(prev) && isNameRune(next))
		}
		if r == '\n' {
			prefix := strings.TrimSpace(value[start:i])
			boundary = !strings.HasSuffix(prefix, ",") && !strings.EqualFold(prefix, "you are")
		}
		if boundary {
			consume(i + n)
		}
	}
	consume(len(value))
	if copied == 0 {
		return value
	}
	b.WriteString(value[copied:])
	return b.String()
}

func isIdentityDeclaration(sentence string, useAliases bool) bool {
	s := strings.TrimSpace(strings.TrimRight(sentence, ".!?;。！？；"))
	if len(s) == 0 || len(s) > maxIdentitySentenceBytes {
		return false
	}
	if embeddedConstraint.MatchString(s) {
		return false
	}
	if intro := runtimeIntro.FindStringIndex(s); intro != nil {
		rest := strings.TrimSpace(s[intro[1]:])
		return useAliases && hasLeadingIdentityAlias(rest)
	}
	for _, intro := range []string{"你是", "你叫", "你的名字是"} {
		if strings.HasPrefix(s, intro) {
			rest := strings.TrimSpace(strings.TrimPrefix(s, intro))
			return useAliases && hasLeadingIdentityAlias(rest) ||
				(strings.Contains(rest, "助手") || strings.Contains(rest, "智能体")) &&
					(strings.Contains(rest, "人工智能") || strings.Contains(rest, "AI") || strings.Contains(rest, "编程")) &&
					(strings.Contains(rest, "开发") || strings.Contains(rest, "打造") || strings.Contains(rest, "运行于"))
		}
	}
	intro := identityIntro.FindStringIndex(s)
	if intro == nil {
		return false
	}
	rest := strings.TrimSpace(s[intro[1]:])
	if forbiddenIdentity.MatchString(rest) {
		return false
	}
	if runtime := runtimeDescription.FindStringIndex(rest); runtime != nil && runtime[0] == 0 {
		if useAliases && hasLeadingIdentityAlias(strings.TrimSpace(rest[runtime[1]:])) {
			return true
		}
	}
	if useAliases && hasLeadingIdentityAlias(rest) {
		return true
	}
	// Unknown names need two independent signals: an explicit proper-name
	// declaration and AI/agent or creator semantics. Generic professional roles
	// and task assignments (reviewer, debugger, planner) therefore stay intact.
	named := unknownLeadingName.MatchString(rest)
	role := agentDescription.MatchString(rest)
	creator := creatorDescription.MatchString(rest)
	environment := runtimeDescription.MatchString(rest) && environmentDescription.MatchString(rest)
	return named && (role || creator) || role && (creator || environment)
}

// introducesSelf accepts any unconstrained "You are ..." sentence. Callers must
// separately prove that it names the identified Agent in a self-identity slot.
func introducesSelf(sentence string) bool {
	s := strings.TrimSpace(strings.TrimRight(sentence, ".!?;。！？；"))
	intro := identityIntro.FindStringIndex(s)
	return intro != nil && len(s) <= maxIdentitySentenceBytes && !embeddedConstraint.MatchString(s) &&
		!forbiddenIdentity.MatchString(strings.TrimSpace(s[intro[1]:]))
}

func hasLeadingIdentityAlias(value string) bool {
	return identityAliasPattern.MatchString(value)
}

func isNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

func identityProtectedRanges(value string) []textRange {
	return classifyPromptProtection(value).all()
}

type promptCodeRange struct {
	textRange
	inline bool
}

type promptProtection struct {
	technical []textRange
	literals  []textRange
	blocks    []textRange
	code      []promptCodeRange
}

func (p promptProtection) all() []textRange {
	ranges := make([]textRange, 0, len(p.technical)+len(p.literals)+len(p.blocks)+len(p.code))
	ranges = append(ranges, p.technical...)
	ranges = append(ranges, p.literals...)
	ranges = append(ranges, p.blocks...)
	for _, code := range p.code {
		ranges = append(ranges, code.textRange)
	}
	return ranges
}

func (p promptProtection) protectedBlock(start, end int) bool {
	if overlapsProtected(start, end, p.blocks) {
		return true
	}
	for _, code := range p.code {
		if !code.inline && start < code.end && end > code.start {
			return true
		}
	}
	return false
}

func classifyPromptProtection(value string) promptProtection {
	p := promptProtection{technical: protectedTextRanges(value)}
	for _, re := range []*regexp.Regexp{inlineQuoteRanges, markdownLinkRanges, xmlIdentifierValue} {
		for _, m := range re.FindAllStringIndex(value, -1) {
			p.literals = append(p.literals, textRange{start: m[0], end: m[1]})
		}
	}
	// XML tags are ASCII, so locate closing tags on the original byte string
	// using RE2 rather than lowercased-text offsets.
	for _, m := range xmlProtectedStart.FindAllStringIndex(value, -1) {
		opener := value[m[0]:m[1]]
		tag := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(opener, "<"), ">"))[0]
		end := len(value)
		if close := regexp.MustCompile(`(?i)</` + regexp.QuoteMeta(tag) + `\s*>`).FindStringIndex(value[m[1]:]); close != nil {
			end = m[1] + close[1]
		}
		p.blocks = append(p.blocks, textRange{start: m[0], end: end})
	}
	fenceStart, fenceChar, fenceWidth := -1, byte(0), 0
	shellFence := false
	// As in CommonMark, an indented code block cannot interrupt a paragraph:
	// indented XML such as an <available_skills> listing stays prose.
	afterBlank, indentedCode := true, false
	for offset := 0; offset < len(value); {
		end := len(value)
		if n := strings.IndexByte(value[offset:], '\n'); n >= 0 {
			end = offset + n + 1
		}
		line := strings.TrimSpace(value[offset:end])
		indented := strings.HasPrefix(value[offset:end], "    ") || strings.HasPrefix(value[offset:end], "\t")
		if fenceStart < 0 && line != "" {
			indentedCode = indented && (indentedCode || afterBlank)
		}
		afterBlank = line == ""
		if strings.HasPrefix(line, ">") || fenceStart < 0 && indentedCode && line != "" {
			p.blocks = append(p.blocks, textRange{start: offset, end: end})
		}
		fenceLine := false
		if len(line) >= 3 && (line[0] == '`' || line[0] == '~') {
			width := 0
			for width < len(line) && line[width] == line[0] {
				width++
			}
			if width >= 3 {
				if fenceStart < 0 {
					fenceLine = true
					fenceStart, fenceChar, fenceWidth = offset, line[0], width
					shellFence = isShellFence(line[width:])
				} else if line[0] == fenceChar && width >= fenceWidth && strings.TrimSpace(line[width:]) == "" {
					fenceLine = true
					span := textRange{start: fenceStart, end: end}
					if shellFence {
						p.blocks = append(p.blocks, span)
					} else {
						p.code = append(p.code, promptCodeRange{span, false})
					}
					fenceStart = -1
				}
			}
		}
		if !fenceLine && fenceStart < 0 && strings.ContainsRune(line, '`') {
			p.code = appendInlineCode(p.code, value[offset:end], offset)
		}
		offset = end
	}
	if fenceStart >= 0 {
		p.blocks = append(p.blocks, textRange{start: fenceStart, end: len(value)})
	}
	return p
}

func appendInlineCode(ranges []promptCodeRange, line string, offset int) []promptCodeRange {
	start, width := -1, 0
	for i := 0; i < len(line); {
		at := strings.IndexByte(line[i:], '`')
		if at < 0 {
			break
		}
		at += i
		i = at + 1
		for i < len(line) && line[i] == '`' {
			i++
		}
		if start < 0 {
			start, width = at, i-at
		} else if i-at == width {
			ranges = append(ranges, promptCodeRange{textRange{offset + start, offset + i}, true})
			start = -1
		}
	}
	return ranges
}

func isShellFence(info string) bool {
	info = strings.TrimSpace(strings.ToLower(info))
	lang, _, _ := strings.Cut(info, " ")
	switch strings.Trim(lang, "{}.") {
	case "sh", "bash", "zsh", "fish", "shell", "shell-session", "console", "terminal", "powershell", "pwsh", "cmd", "bat":
		return true
	}
	return false
}
