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

// This catalogue contains product names, not invented system-prompt templates.
//
//go:embed assets/agent-brands.json
var brandCatalogueJSON []byte

type agentBrand struct {
	Name   string `json:"name"`
	Family string `json:"family"`
	// Ambiguous names are also ordinary words (cursor, goose, pi). They are
	// only rewritten for the agent that declared itself with that name.
	Ambiguous bool `json:"ambiguous"`
	// IdentityOnly names never join the legacy substring keyword preset.
	IdentityOnly bool `json:"identity_only"`
	// Vendors are companies credited for the product. They are rewritten
	// only in attribution phrases ("built by X"), never in API or policy names.
	Vendors []string `json:"vendors"`
}

func (b agentBrand) family() string {
	if b.Family != "" {
		return b.Family
	}
	return b.Name
}

var agentBrands = func() []agentBrand {
	var brands []agentBrand
	if err := json.Unmarshal(brandCatalogueJSON, &brands); err != nil {
		panic(err)
	}
	return brands
}()

func catalogRewriteMappings() []config.Mapping {
	var mappings []config.Mapping
	for _, brand := range agentBrands {
		if !brand.IdentityOnly {
			mappings = append(mappings, config.Mapping{Match: brand.Name, Replacement: "Antigravity"})
		}
	}
	return mappings
}

type brandPattern struct {
	brand      agentBrand
	identifier *regexp.Regexp
	prose      *regexp.Regexp
	needle     string
}

type brandCatalog struct{ patterns []brandPattern }

func compileBrandCatalog(brands []agentBrand) *brandCatalog {
	brands = append([]agentBrand(nil), brands...)
	sort.SliceStable(brands, func(i, j int) bool { return len(brands[i].Name) > len(brands[j].Name) })
	c := &brandCatalog{}
	for _, brand := range brands {
		parts := strings.Fields(brand.Name)
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		c.patterns = append(c.patterns, brandPattern{
			brand, regexp.MustCompile(`(?i)` + strings.Join(parts, `[ _-]+`)),
			regexp.MustCompile(`(?i)` + strings.Join(parts, `[[:space:]\p{Zs}]+`)), strings.ToLower(strings.Fields(brand.Name)[0]),
		})
	}
	return c
}

var defaultBrandCatalog = compileBrandCatalog(agentBrands)

// A self-identity places the product name directly after a declaration of
// who the agent is or where it runs, within the same sentence. Observed forms:
// "You are Codex", "running in the Codex CLI", "running as a coding agent in",
// "an AI agent called goose", "You operate exclusively in Cursor", "你是 X".
var selfIdentityContext = regexp.MustCompile(`(?i)(?:\byou\s+are(?:\s+(?:called|named))?|\byour\s+name\s+is|\b(?:agent|assistant|harness|you\s+are)\b[^.;!?\n]{0,48}?\b(?:called|named)|\b(?:running|operating|working|embedded|integrated)(?:\s+as\s+[^.;!?\n]{1,48}?)?\s+(?:in|inside|within)|\byou\s+(?:operate|run|work)(?:\s+[a-z]+ly)?\s+(?:in|inside|within))\s+(?:the\s+)?$|(?:你是|你叫|运行在|运行于)\s*$`)

func selfIdentity(text string, start int) bool {
	left := start
	for left > 0 && start-left < 160 && !strings.ContainsRune("\n;!?。；", rune(text[left-1])) {
		// A dot ends the sentence only before whitespace ("GPT-5.1" does not).
		if text[left-1] == '.' && left < len(text) && (text[left] == ' ' || text[left] == '\n') {
			break
		}
		left--
	}
	return selfIdentityContext.MatchString(text[left:start])
}

// agentProfile is layer one: which agent sent this request, decided once from
// its own system/developer instructions. Without a profile nothing is aliased.
type agentProfile struct {
	catalog      *brandCatalog
	families     map[string]bool
	traceMatches map[string]bool
	attribution  *[]attributionRule
}

func (c *brandCatalog) identify(texts []string) *agentProfile {
	p := &agentProfile{catalog: c, families: map[string]bool{}}
	for _, text := range texts {
		lower := strings.ToLower(text)
		protected := agentPromptProtection(text).all()
		for _, pat := range c.patterns {
			if !strings.Contains(lower, pat.needle) {
				continue
			}
			for _, hit := range pat.prose.FindAllStringIndex(text, -1) {
				start, end := hit[0], hit[1]
				if !proseBrandBoundary(text, start, end) || overlapsProtected(start, end, protected) || !selfIdentity(text, start) {
					continue
				}
				if technicalBrandUse(text, start, end) {
					continue
				}
				p.families[pat.brand.family()] = true
			}
		}
	}
	if len(p.families) == 0 {
		return nil
	}
	return p
}

func brandBoundary(text string, start, end int, nameRune func(rune) bool) bool {
	left, _ := utf8.DecodeLastRuneInString(text[:start])
	right, _ := utf8.DecodeRuneInString(text[end:])
	return (start == 0 || !nameRune(left)) && (end == len(text) || !nameRune(right))
}

func brandWordRune(r rune) bool {
	// A Latin brand can be a complete word inside unspaced Chinese prose.
	return unicode.IsLetter(r) && !unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) || unicode.IsNumber(r) || unicode.IsMark(r)
}

func proseBrandBoundary(text string, start, end int) bool {
	return brandBoundary(text, start, end, func(r rune) bool { return brandWordRune(r) || r == '_' || r == '$' })
}

func aliasCase(match string) string {
	if match == strings.ToLower(match) {
		return "antigravity"
	}
	if match == strings.ToUpper(match) {
		return "ANTIGRAVITY"
	}
	return "Antigravity"
}

// aliasName rewrites a complete executable identifier (tool name, namespace,
// Code Mode path segment). The structure already proves it is an identifier,
// so the identified agent's names are replaced directly, ambiguous or not.
func (p *agentProfile) aliasName(name string) string {
	if isPluginPath(name) {
		return name
	}
	var edits []promptEdit
	lower := strings.ToLower(name)
	for _, pat := range p.catalog.patterns {
		own := p.families[pat.brand.family()]
		if pat.brand.Ambiguous && !own || !strings.Contains(lower, pat.needle) {
			continue
		}
		for _, hit := range pat.identifier.FindAllStringIndex(name, -1) {
			start, end := hit[0], hit[1]
			if brandBoundary(name, start, end, isToolWordRune) {
				edits = addPromptEdit(edits, start, end, aliasCase(name[start:end]))
			}
		}
	}
	return applyPromptEdits(name, edits)
}

func isToolWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '$'
}
