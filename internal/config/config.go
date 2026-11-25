package config

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	PluginName       = "antigravity-prompt-bridge"
	PluginVersion    = "0.5.5"
	PluginRepository = "https://github.com/yurhett/antigravity-prompt-bridge"
)

type Mode string

const (
	ModeRewrite Mode = "rewrite"
)

// GuardMode decides what happens when an identified Agent's request misses
// a rule. Gaps leak agent features; drift is already handled by a fallback
// rule (identity normalization) and only means a recorded template is stale.
type GuardMode string

const (
	GuardOff    GuardMode = "off"    // no coverage handling
	GuardReport GuardMode = "report" // log every miss, forward
	GuardReject GuardMode = "reject" // refuse gaps, log drift
	GuardStrict GuardMode = "strict" // refuse gaps and drift (development)
)

// UnidentifiedMode decides what happens when agent_alias is on and no Agent
// is identified. With agent_alias off every request takes the legacy path.
type UnidentifiedMode string

const (
	UnidentifiedLegacy UnidentifiedMode = "legacy" // legacy keyword or heuristic rewrite
	UnidentifiedReject UnidentifiedMode = "reject" // refuse known Agent fingerprints, else add the native identity
)

type Mapping struct {
	Match       string
	Replacement string
}

type Config struct {
	Mode                          Mode
	UseDefaultKeywords            bool
	CustomMappings                []Mapping
	HeuristicIdentity             bool
	EnableAgy                     bool
	EnableAntigravity             bool
	AntigravityBridgeSystemPrompt bool
	AgentAlias                    bool
	BrandGuard                    GuardMode
	Unidentified                  UnidentifiedMode
	Audit                         bool
}

func Default() Config {
	return Config{Mode: ModeRewrite, UseDefaultKeywords: true, EnableAgy: true, EnableAntigravity: true, AntigravityBridgeSystemPrompt: true, AgentAlias: true, BrandGuard: GuardReject, Unidentified: UnidentifiedReject}
}

func Parse(raw []byte) (Config, error) {
	cfg := Default()
	if len(strings.TrimSpace(string(raw))) == 0 {
		return cfg, nil
	}

	var values map[string]any
	if err := yaml.Unmarshal(raw, &values); err != nil {
		return Config{}, fmt.Errorf("decode config yaml: %w", err)
	}
	if value, exists := values["audit"]; exists {
		v, ok := value.(bool)
		if !ok {
			return Config{}, fmt.Errorf("audit must be a boolean")
		}
		cfg.Audit = v
	}
	if value, exists := values["heuristic_identity"]; exists {
		v, ok := value.(bool)
		if !ok {
			return Config{}, fmt.Errorf("heuristic_identity must be a boolean")
		}
		cfg.HeuristicIdentity = v
	}
	for key, destination := range map[string]*bool{"enable_agy": &cfg.EnableAgy, "enable_antigravity": &cfg.EnableAntigravity, "antigravity_bridge_system_prompt": &cfg.AntigravityBridgeSystemPrompt, "agent_alias": &cfg.AgentAlias} {
		if value, exists := values[key]; exists {
			v, ok := value.(bool)
			if !ok {
				return Config{}, fmt.Errorf("%s must be a boolean", key)
			}
			*destination = v
		}
	}
	if value, exists := values["mode"]; exists {
		text, ok := value.(string)
		if !ok {
			return Config{}, fmt.Errorf("mode must be a string")
		}
		cfg.Mode = Mode(strings.ToLower(strings.TrimSpace(text)))
		if cfg.Mode != ModeRewrite {
			return Config{}, fmt.Errorf("mode must be rewrite")
		}
	}
	guard, err := parseEnum(values, "brand_guard", string(cfg.BrandGuard), "off", "report", "reject", "strict")
	if err != nil {
		return Config{}, err
	}
	cfg.BrandGuard = GuardMode(guard)
	unidentified, err := parseEnum(values, "unidentified", string(cfg.Unidentified), "legacy", "reject")
	if err != nil {
		return Config{}, err
	}
	cfg.Unidentified = UnidentifiedMode(unidentified)
	if value, exists := values["use_default_keywords"]; exists {
		boolValue, ok := value.(bool)
		if !ok {
			return Config{}, fmt.Errorf("use_default_keywords must be a boolean")
		}
		cfg.UseDefaultKeywords = boolValue
	}
	if value, exists := values["custom_mappings"]; exists {
		mappings, err := parseCustomMappings(value)
		if err != nil {
			return Config{}, err
		}
		cfg.CustomMappings = mappings
	}
	return cfg, nil
}

func parseEnum(values map[string]any, key, fallback string, allowed ...string) (string, error) {
	value, exists := values[key]
	if !exists {
		return fallback, nil
	}
	text, ok := value.(string)
	if off, isBool := value.(bool); isBool && !off {
		// A YAML 1.1 writer may turn a bare `off` into false.
		text, ok = "off", true
	}
	text = strings.ToLower(strings.TrimSpace(text))
	for _, candidate := range allowed {
		if ok && text == candidate {
			return text, nil
		}
	}
	return "", fmt.Errorf("%s must be one of %s", key, strings.Join(allowed, ", "))
}

func parseCustomMappings(value any) ([]Mapping, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return parseMappingString(typed)
	case map[string]any:
		mappings := make([]Mapping, 0, len(typed))
		for match, replacement := range typed {
			text, ok := replacement.(string)
			if !ok {
				return nil, fmt.Errorf("custom_mappings values must be strings")
			}
			mappings = append(mappings, Mapping{Match: match, Replacement: text})
		}
		return mappings, nil
	case []any:
		mappings := make([]Mapping, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("custom_mappings entries must be strings")
			}
			parsed, err := parseMappingString(text)
			if err != nil {
				return nil, err
			}
			mappings = append(mappings, parsed...)
		}
		return mappings, nil
	default:
		return nil, fmt.Errorf("custom_mappings must be an object, array, or string")
	}
}

func parseMappingString(value string) ([]Mapping, error) {
	entries := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	mappings := make([]Mapping, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		match, replacement, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("custom_mappings entries must use match: replacement")
		}
		mappings = append(mappings, Mapping{Match: match, Replacement: replacement})
	}
	return mappings, nil
}

func NormalizeMappings(mappings []Mapping) []Mapping {
	seen := make(map[string]struct{}, len(mappings))
	reversed := make([]Mapping, 0, len(mappings))
	for i := len(mappings) - 1; i >= 0; i-- {
		match := strings.ToLower(strings.TrimSpace(mappings[i].Match))
		replacement := strings.TrimSpace(mappings[i].Replacement)
		if match == "" || replacement == "" {
			continue
		}
		if _, exists := seen[match]; exists {
			continue
		}
		seen[match] = struct{}{}
		reversed = append(reversed, Mapping{Match: match, Replacement: replacement})
	}
	out := make([]Mapping, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		out = append(out, reversed[i])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Match) == len(out[j].Match) {
			return out[i].Match < out[j].Match
		}
		return len(out[i].Match) > len(out[j].Match)
	})
	return out
}
