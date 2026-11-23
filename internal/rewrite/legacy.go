package rewrite

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

var defaultRewriteMappings = catalogRewriteMappings()

func effectiveMappings(cfg config.Config) []config.Mapping {
	mappings := make([]config.Mapping, 0, len(defaultRewriteMappings)+len(cfg.CustomMappings))
	if cfg.UseDefaultKeywords {
		mappings = append(mappings, defaultRewriteMappings...)
	}
	mappings = append(mappings, cfg.CustomMappings...)
	return config.NormalizeMappings(mappings)
}

func rewriteRequestBodyWithConfig(body []byte, cfg config.Config) ([]byte, bool) {
	root, err := protocol.DecodeJSON(body)
	if err != nil {
		return nil, false
	}
	mappings := effectiveMappings(cfg)
	rewritten, changed := protocol.TransformPromptFields(root, func(text string) (string, bool) {
		if cfg.HeuristicIdentity {
			return rewriteIdentityPrompt(text, cfg)
		}
		return rewritePromptText(text, mappings)
	})
	if !changed {
		return nil, false
	}
	raw, err := json.Marshal(rewritten)
	if err != nil {
		return nil, false
	}
	return raw, true
}

func rewritePromptText(value string, mappings []config.Mapping) (string, bool) {
	next := value
	changed := false
	for _, mapping := range mappings {
		var replaced bool
		next, replaced = replaceInsensitiveProtected(next, mapping.Match, mapping.Replacement)
		changed = changed || replaced
	}
	return next, changed
}

func replaceInsensitiveProtected(value, match, replacement string) (string, bool) {
	if match == "" {
		return value, false
	}
	lowerValue := strings.ToLower(value)
	lowerMatch := strings.ToLower(match)
	protected := protectedTextRanges(value)
	var builder strings.Builder
	start := 0
	changed := false
	for {
		index := strings.Index(lowerValue[start:], lowerMatch)
		if index < 0 {
			break
		}
		index += start
		builder.WriteString(value[start:index])
		end := index + len(match)
		if overlapsProtected(index, end, protected) {
			builder.WriteString(value[index:end])
		} else {
			builder.WriteString(replacement)
			changed = true
		}
		start = end
	}
	if !changed {
		return value, false
	}
	builder.WriteString(value[start:])
	return builder.String(), true
}

type textRange struct {
	start int
	end   int
}

var protectedTextPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:https?|ftp)://[^\s"'<>]+`),
	regexp.MustCompile(`(?i)\bmcp(?:__|\\_\\_)[A-Za-z0-9][A-Za-z0-9_\\-]*`),
	regexp.MustCompile(`(?i)\b[A-Za-z]:[\\/][^\s"'<>]+`),
	regexp.MustCompile(`(?:\\\\|//)[^\s"'<>]+`),
	regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])/(?:[^\s"'<>/]+/)+[^\s"'<>]*`),
	regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(?:[A-Za-z0-9._~-]+[\\/])+[A-Za-z0-9._~-]+`),
	regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(?:~|\.{1,2})[\\/][^\s"'<>]+`),
	regexp.MustCompile(`(?i)\.codex(?:[^A-Za-z0-9]|$)`),
	regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])[A-Za-z0-9_-]+\.(?:md|markdown)`),
}

func protectedTextRanges(value string) []textRange {
	var ranges []textRange
	for _, pattern := range protectedTextPatterns {
		for _, match := range pattern.FindAllStringIndex(value, -1) {
			ranges = append(ranges, textRange{start: match[0], end: match[1]})
		}
	}
	return ranges
}

func overlapsProtected(start, end int, ranges []textRange) bool {
	for _, protected := range ranges {
		if start < protected.end && protected.start < end {
			return true
		}
	}
	return false
}
