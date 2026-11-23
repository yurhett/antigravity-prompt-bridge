package config

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	PluginName       = "antigravity-prompt-bridge"
	PluginVersion    = "0.5.4"
	PluginRepository = "https://github.com/yurhett/antigravity-prompt-bridge"
)

type Mode string

const (
	ModeRewrite Mode = "rewrite"
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
	Audit                         bool
}

func Default() Config {
	return Config{Mode: ModeRewrite, UseDefaultKeywords: true, EnableAgy: true, EnableAntigravity: true, AntigravityBridgeSystemPrompt: true, AgentAlias: true}
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
