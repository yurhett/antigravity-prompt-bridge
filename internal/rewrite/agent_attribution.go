package rewrite

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const nativeAttributionCredit = "the Google Deepmind team"

type attributionRule struct {
	pattern *regexp.Regexp
	replace func(text string, match []int) string
}

// attributionRules credits the identified Agent's own vendor or product names
// to the native team. Only attribution phrases qualify, so "the Anthropic API"
// or "Microsoft content policies" keep their functional meaning.
func (p *agentProfile) attributionRules() []attributionRule {
	if p.attribution != nil {
		return *p.attribution
	}
	var names []string
	seen := map[string]bool{}
	add := func(name string) {
		if key := strings.ToLower(name); name != "" && !seen[key] {
			seen[key] = true
			names = append(names, name)
		}
	}
	for _, brand := range agentBrands {
		if !p.families[brand.family()] {
			continue
		}
		add(brand.Name)
		for _, vendor := range brand.Vendors {
			add(vendor)
		}
	}
	rules := []attributionRule{}
	if len(names) > 0 {
		sort.SliceStable(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
		for i, name := range names {
			parts := strings.Fields(name)
			for j := range parts {
				parts[j] = regexp.QuoteMeta(parts[j])
			}
			names[i] = strings.Join(parts, `\s+`)
		}
		vendor := `(?P<name>` + strings.Join(names, "|") + `)`
		keep := func(group int, suffix string) func(string, []int) string {
			return func(text string, m []int) string { return text[m[2*group]:m[2*group+1]] + suffix }
		}
		rules = []attributionRule{
			{regexp.MustCompile(`(?i)\b(built|created|developed|designed|made|led|maintained|trained|published|released|provided|engineered|produced|owned|operated|offered)\s+by\s+(?:the\s+)?` + vendor + `(?:\s+team)?`),
				keep(1, " by "+nativeAttributionCredit)},
			{regexp.MustCompile(`(?i)\b(an?)\s+(product|project|tool|service|offering)\s+(?:of|from)\s+(?:the\s+)?` + vendor + `(?:\s+team)?`),
				func(text string, m []int) string {
					return text[m[2]:m[2]+1] + " " + text[m[4]:m[5]] + " of " + nativeAttributionCredit
				}},
			{regexp.MustCompile(`(?i)` + vendor + `['’]s\s+((?:official|own)\s+(?:AI\s+|coding\s+)?(?:CLI|agent|assistant|tool|app|IDE|editor|extension|harness|product))\b`),
				func(text string, m []int) string {
					article := "the "
					if before := strings.TrimRight(text[:m[0]], " \t"); before == "" || strings.ContainsAny(before[len(before)-1:], ".!?\n") {
						article = "The "
					}
					return article + text[m[4]:m[5]]
				}},
			{regexp.MustCompile(`(?i)[ \t]*\((?:formerly|previously)(?:\s+known\s+as)?\s+` + vendor + `\)`),
				func(string, []int) string { return "" }},
			{regexp.MustCompile(`(?i)` + vendor + `\s*(开发|打造|推出|出品|研发|构建)`),
				func(text string, m []int) string {
					credit := "Google Deepmind 团队"
					if before, _ := utf8.DecodeLastRuneInString(text[:m[0]]); m[0] > 0 && !unicode.IsSpace(before) && !unicode.IsPunct(before) {
						credit = " " + credit
					}
					return credit + text[m[4]:m[5]]
				}},
		}
	}
	p.attribution = &rules
	return rules
}

func (p *agentProfile) rewriteAttributions(text string, protected []textRange) (string, int) {
	var edits []promptEdit
	for _, rule := range p.attributionRules() {
		name := rule.pattern.SubexpIndex("name")
		for _, m := range rule.pattern.FindAllStringSubmatchIndex(text, -1) {
			start, end := m[0], m[1]
			if overlapsProtected(start, end, protected) || !proseBrandBoundary(text, m[2*name], m[2*name+1]) ||
				technicalBrandUse(text, m[2*name], m[2*name+1]) {
				continue
			}
			edits = addPromptEdit(edits, start, end, rule.replace(text, m))
		}
	}
	return applyPromptEdits(text, edits), len(edits)
}
