package rewrite

import (
	"fmt"
	"strings"

	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

type CoverageLevel string

const (
	CoverageDrift CoverageLevel = "drift"
	CoverageGap   CoverageLevel = "gap"
)

// CoverageCheck is one rule the identified Agent's request was expected to
// hit. Coverage counts rule hits instead of scanning the prompt for brand
// names, so project text quoted into a system prompt never fails a request
// and intentionally preserved code, paths and commands are not gaps.
type CoverageCheck struct {
	Rule     string        `json:"rule"`
	Expected int           `json:"expected"`
	Hits     int           `json:"hits"`
	Level    CoverageLevel `json:"level"`
}

func (c CoverageCheck) String() string { return fmt.Sprintf("%s (%d/%d)", c.Rule, c.Hits, c.Expected) }

type coverageCounter struct {
	checks map[string]*CoverageCheck
	order  []string
}

func newCoverageCounter() *coverageCounter {
	return &coverageCounter{checks: map[string]*CoverageCheck{}}
}

func (c *coverageCounter) expect(rule string, n int, level CoverageLevel) {
	if c == nil || n <= 0 {
		return
	}
	check := c.checks[rule]
	if check == nil {
		check = &CoverageCheck{Rule: rule, Level: level}
		c.checks[rule] = check
		c.order = append(c.order, rule)
	}
	check.Expected += n
}

// add merges a rule an adapter plan counted itself.
func (c *coverageCounter) add(check CoverageCheck) {
	c.expect(check.Rule, check.Expected, check.Level)
	c.hit(check.Rule, check.Hits)
}

// hit records hits only for rules this request expects.
func (c *coverageCounter) hit(rule string, n int) {
	if c == nil || n <= 0 {
		return
	}
	if check := c.checks[rule]; check != nil {
		check.Hits += n
	}
}

// misses lists, in expectation order, every rule hit fewer times than expected.
func (c *coverageCounter) misses() []CoverageCheck {
	if c == nil {
		return nil
	}
	var out []CoverageCheck
	for _, rule := range c.order {
		if check := c.checks[rule]; check.Hits < check.Expected {
			out = append(out, *check)
		}
	}
	return out
}

// CoverageSummary summarizes the missed rules without exposing prompt text.
func CoverageSummary(misses []CoverageCheck) string {
	parts := make([]string, len(misses))
	for i, miss := range misses {
		parts[i] = string(miss.Level) + " " + miss.String()
	}
	return "Agent rewrite coverage: " + strings.Join(parts, ", ")
}

func identityRule(family string) string         { return "identity:" + family }
func identityTemplateRule(family string) string { return "identity-template:" + family }
func adapterRule(id string) string              { return "adapter:" + id }

func isIdentityTemplate(t agentPromptTemplate) bool {
	return strings.Contains(t.Replacement, nativeAntigravityIdentity)
}

// expectCoverage lists what an identified request must hit:
//   - identity:F, the Agent's self-introduction became the native identity,
//     by a template or by identity normalization;
//   - identity-template:F, one of F's recorded identity templates matched,
//     so a reworded client prompt shows up as template drift;
//   - adapter:ID, an adapter whose feature is declared in the request built
//     a plan (the OpenCode Code Mode `execute(code)` tool needs its catalog).
func expectCoverage(c *coverageCounter, view protocol.AgentRequestView, profile *agentProfile) {
	for _, registration := range registeredAgentAdapters {
		family := registration.family
		if !profile.families[family] {
			continue
		}
		// A self-introduction left in place keeps vendor framing: a gap.
		c.expect(identityRule(family), 1, CoverageGap)
		for _, template := range registration.templates {
			if isIdentityTemplate(template) && (!template.traceOnly || profile.traceMatches[family]) {
				// Normalization already replaced the sentence; only the
				// recorded template is out of date.
				c.expect(identityTemplateRule(family), 1, CoverageDrift)
				break
			}
		}
		if adapter := registration.execution; adapter != nil && adapter.Declared(view) {
			c.expect(adapterRule(adapter.ID()), 1, CoverageGap)
		}
	}
}
