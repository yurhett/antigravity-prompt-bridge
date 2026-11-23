package plugin

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/rewrite"
)

// Plugin owns the configuration and in-flight aliases for one CPA instance.
type Plugin struct {
	hostCall func(string, any) (json.RawMessage, error)
	configMu sync.RWMutex
	config   config.Config
	aliases  aliasStateStore
}

func New(hostCall func(string, any) (json.RawMessage, error)) *Plugin {
	return &Plugin{hostCall: hostCall, config: config.Default(), aliases: aliasStateStore{states: make(map[string]*rewrite.AliasState)}}
}

func (p *Plugin) Shutdown() { p.aliases.clear() }

func (p *Plugin) setConfig(cfg config.Config) {
	cfg.CustomMappings = config.NormalizeMappings(cfg.CustomMappings)
	p.configMu.Lock()
	defer p.configMu.Unlock()
	p.config = cfg
}

func (p *Plugin) configSnapshot() config.Config {
	p.configMu.RLock()
	defer p.configMu.RUnlock()
	cfg := p.config
	cfg.CustomMappings = slices.Clone(cfg.CustomMappings)
	return cfg
}

func (p *Plugin) Call(method string, request []byte) ([]byte, int) {
	switch method {
	case pluginabi.MethodPluginRegister:
		return p.handleLifecycle(request), 0
	case pluginabi.MethodPluginReconfigure:
		return p.handleLifecycle(request), 0
	case pluginabi.MethodRequestInterceptBefore:
		return handleRequestInterceptBefore(request), 0
	case pluginabi.MethodRequestInterceptAfter:
		return p.handleRequestAfter(request), 0
	case pluginabi.MethodResponseInterceptAfter:
		return p.handleAliasResponse(request), 0
	case pluginabi.MethodResponseInterceptStreamChunk:
		return p.handleAliasStream(request), 0
	case pluginabi.MethodRequestComplete:
		var req pluginapi.RequestCompletion
		if err := json.Unmarshal(request, &req); err != nil {
			return ErrorEnvelope("invalid_request", err.Error()), 0
		}
		p.aliases.remove(req.RequestID)
		return mustEnvelope(struct{}{}), 0
	default:
		return ErrorEnvelope("unknown_method", fmt.Sprintf("unknown method %q", method)), 0
	}
}

func (p *Plugin) handleLifecycle(request []byte) []byte {
	if len(request) > 0 {
		cfg, err := parseLifecycleConfig(request)
		if err != nil {
			return ErrorEnvelope("invalid_config", err.Error())
		}
		p.setConfig(cfg)
	}
	return mustEnvelope(registrationResponse())
}

func registrationResponse() any {
	return struct {
		SchemaVersion uint32             `json:"schema_version"`
		Metadata      pluginapi.Metadata `json:"metadata"`
		Capabilities  map[string]bool    `json:"capabilities"`
	}{pluginabi.SchemaVersion, pluginapi.Metadata{Name: config.PluginName, Version: config.PluginVersion, Author: "yurhett", GitHubRepository: config.PluginRepository, ConfigFields: configFields()}, map[string]bool{"request_interceptor": true, "response_interceptor": true, "response_stream_interceptor": true, "request_lifecycle_plugin": true}}
}
func configFields() []pluginapi.ConfigField {
	return []pluginapi.ConfigField{
		{Name: "mode", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"rewrite"}, Description: "Rewrite selected system/developer prompts before upstream execution."},
		{Name: "enable_agy", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Apply this filter to selected agy credentials. Default true."},
		{Name: "enable_antigravity", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Apply this filter to selected native antigravity credentials independently. Default true."},
		{Name: "antigravity_bridge_system_prompt", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Native antigravity only: move rewritten system/developer prompts into a USER_REQUEST user message, keeping the native identity as system. Does not affect agy. Default true."},
		{Name: "agent_alias", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Identify the agent from its system/developer self-declaration. Apply prompt templates, whole-word brand cleanup and reversible tool aliases. Unidentified requests use the legacy rewrite settings. Default true."},
		{Name: "use_default_keywords", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Use the legacy keyword preset or use its names as clues for heuristic identity normalization. Default true."},
		{Name: "custom_mappings", Type: pluginapi.ConfigFieldTypeObject, Description: "Case-insensitive custom mappings; override built-in mappings."},
		{Name: "heuristic_identity", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Conservatively normalize identity and harness declarations to the native Antigravity identity; preserve quoted/code/project content. Default false."},
		{Name: "audit", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Log request ID, body SHA256 hashes, identified agent families and rewrite counts. Prompt contents are excluded."},
	}
}

func handleRequestInterceptBefore(request []byte) []byte {
	var req pluginapi.RequestInterceptRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return ErrorEnvelope("invalid_request", fmt.Sprintf("decode request.intercept_before request: %v", err))
	}
	return mustEnvelope(pluginapi.RequestInterceptResponse{})
}

func (p *Plugin) handleRequestAfter(request []byte) []byte {
	var req pluginapi.RequestInterceptRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return ErrorEnvelope("invalid_request", fmt.Sprintf("decode request.intercept_after request: %v", err))
	}
	// A retry may select a different provider or no longer require aliases.
	p.aliases.remove(req.RequestID)
	// Resolve the actual selected credential, including unprefixed models and aliases.
	// Never infer provider from the OpenAI wire format or caller-controlled model name.
	index, _ := req.Metadata["selected_auth_index"].(string)
	if strings.TrimSpace(index) == "" {
		return mustEnvelope(pluginapi.RequestInterceptResponse{})
	}
	raw, err := p.hostCall("host.auth.get_runtime", map[string]any{"auth_index": index})
	if err != nil {
		_, _ = p.hostCall("host.log", map[string]any{"level": "warn", "message": config.PluginName + ": provider lookup failed; request unchanged", "fields": map[string]any{"request_id": req.RequestID}})
		return mustEnvelope(pluginapi.RequestInterceptResponse{})
	}
	var runtime struct {
		Auth struct {
			Provider string `json:"provider"`
		} `json:"auth"`
	}
	if json.Unmarshal(raw, &runtime) != nil {
		return mustEnvelope(pluginapi.RequestInterceptResponse{})
	}
	cfg := p.configSnapshot()
	if !providerEnabled(runtime.Auth.Provider, cfg) {
		p.aliases.remove(req.RequestID)
		return mustEnvelope(pluginapi.RequestInterceptResponse{})
	}
	agent, err := rewrite.Request(req.Body, cfg)
	if cfg.AgentAlias {
		state := agent.State
		if err == nil && state != nil && req.Stream && !rewrite.SupportsStreamFormat(req.SourceFormat) {
			err = fmt.Errorf("unsupported agent alias stream format")
		}
		if err == nil && state != nil && strings.TrimSpace(req.RequestID) == "" {
			err = fmt.Errorf("Agent aliasing requires a request ID for response restoration")
		}
		if err == nil && state != nil {
			err = p.aliases.put(req.RequestID, state)
		}
		if err != nil {
			code := "agent_alias_conflict"
			if _, ok := err.(*rewrite.AdapterError); ok {
				code = "agent_alias_adapter_unsupported"
			}
			payload, _ := json.Marshal(map[string]any{"error": map[string]any{"type": "invalid_request_error", "code": code, "message": err.Error()}})
			return mustEnvelope(pluginapi.RequestInterceptResponse{Terminate: true, StatusCode: 400, ResponseBody: payload})
		}
	}
	body := agent.Body
	rewritten := body != nil
	if !rewritten {
		body = req.Body
	}
	bridgeEnabled := cfg.AntigravityBridgeSystemPrompt && strings.EqualFold(strings.TrimSpace(runtime.Auth.Provider), "antigravity")
	if bridgeEnabled {
		if bridged, moved := rewrite.BridgeNativeSystemPrompt(body); moved {
			body, rewritten = bridged, true
		}
	}
	if cfg.Audit && (rewritten || agent.Stats.ResidualProtected > 0) {
		fields := map[string]any{
			"request_id": req.RequestID, "before_sha256": fmt.Sprintf("%x", sha256.Sum256(req.Body)), "after_sha256": fmt.Sprintf("%x", sha256.Sum256(body)),
			"native_bridge_enabled": bridgeEnabled, "agent_alias_enabled": cfg.AgentAlias,
		}
		if len(agent.Families) > 0 {
			fields["agent_families"] = agent.Families
			fields["template_hits"] = agent.Stats.TemplateHits
			fields["residual_fixed"] = agent.Stats.ResidualFixed
			fields["residual_protected"] = agent.Stats.ResidualProtected
		}
		_, _ = p.hostCall("host.log", map[string]any{"level": "info", "message": config.PluginName + ": processed upstream request", "fields": fields})
	}
	if !rewritten {
		return mustEnvelope(pluginapi.RequestInterceptResponse{})
	}
	return mustEnvelope(pluginapi.RequestInterceptResponse{Body: body})
}

func mustEnvelope(result any) []byte {
	raw, err := json.Marshal(pluginabi.Envelope{OK: true, Result: mustRawMessage(result)})
	if err != nil {
		return ErrorEnvelope("marshal_error", err.Error())
	}
	return raw
}

func ErrorEnvelope(code, message string) []byte {
	return mustHTTPErrorEnvelope(code, message, 0)
}

func mustHTTPErrorEnvelope(code, message string, status int) []byte {
	raw, err := json.Marshal(pluginabi.Envelope{OK: false, Error: &pluginabi.Error{
		Code:       code,
		Message:    message,
		HTTPStatus: status,
	}})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"marshal_error","message":"failed to encode plugin response"}}`)
	}
	return raw
}

func mustRawMessage(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

func parseLifecycleConfig(request []byte) (config.Config, error) {
	var req lifecycleRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return config.Config{}, fmt.Errorf("decode lifecycle request: %w", err)
	}
	return config.Parse(req.ConfigYAML)
}

func providerEnabled(provider string, cfg config.Config) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "agy":
		return cfg.EnableAgy
	case "antigravity":
		return cfg.EnableAntigravity
	default:
		return false
	}
}
