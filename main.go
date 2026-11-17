package main

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxy_plugin_call(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxy_plugin_free(void*, size_t);
extern void cliproxy_plugin_shutdown(void);
static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
    stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
    if (stored_host == NULL || stored_host->call == NULL) {
        return 1;
    }
    return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
    if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
        stored_host->free_buffer(ptr, len);
    }
}
*/
import "C"

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"regexp"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

const abiVersion = 1

const (
	pluginName       = "antigravity-prompt-bridge"
	pluginVersion    = "0.3.1"
	pluginRepository = "https://github.com/yurhett/antigravity-prompt-bridge"
)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = abiVersion
	plugin.call = (C.cliproxy_plugin_call_fn)(C.cliproxy_plugin_call)
	plugin.free_buffer = (C.cliproxy_plugin_free_fn)(C.cliproxy_plugin_free)
	plugin.shutdown = (C.cliproxy_plugin_shutdown_fn)(C.cliproxy_plugin_shutdown)
	return 0
}

//export cliproxy_plugin_call
func cliproxy_plugin_call(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeCResponse(response, mustErrorEnvelope("invalid_method", "method is required"))
		return 1
	}

	methodName := C.GoString(method)
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}

	raw, code := handlePluginCall(methodName, requestBytes)
	writeCResponse(response, raw)
	return C.int(code)
}

//export cliproxy_plugin_free
func cliproxy_plugin_free(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxy_plugin_shutdown
func cliproxy_plugin_shutdown() {}

func writeCResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.malloc(C.size_t(len(raw)))
	if ptr == nil {
		return
	}
	C.memcpy(ptr, unsafe.Pointer(&raw[0]), C.size_t(len(raw)))
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

func handlePluginCall(method string, request []byte) ([]byte, int) {
	switch method {
	case pluginabi.MethodPluginRegister:
		return handlePluginLifecycle(request), 0
	case pluginabi.MethodPluginReconfigure:
		return handlePluginLifecycle(request), 0
	case pluginabi.MethodRequestInterceptBefore:
		return handleRequestInterceptBefore(request), 0
	case pluginabi.MethodRequestInterceptAfter:
		return handleRequestInterceptAfter(request), 0
	default:
		return mustErrorEnvelope("unknown_method", fmt.Sprintf("unknown method %q", method)), 0
	}
}

func handlePluginLifecycle(request []byte) []byte {
	if len(request) > 0 {
		cfg, err := filterConfigFromLifecycleRequest(request)
		if err != nil {
			return mustErrorEnvelope("invalid_config", err.Error())
		}
		applyFilterConfig(cfg)
	}
	return mustEnvelope(registrationResponse())
}

func registrationResponse() any {
	return struct {
		SchemaVersion uint32             `json:"schema_version"`
		Metadata      pluginapi.Metadata `json:"metadata"`
		Capabilities  map[string]bool    `json:"capabilities"`
	}{pluginabi.SchemaVersion, pluginapi.Metadata{Name: pluginName, Version: pluginVersion, Author: "yurhett", GitHubRepository: pluginRepository, ConfigFields: configFields()}, map[string]bool{"request_interceptor": true}}
}
func configFields() []pluginapi.ConfigField {
	return []pluginapi.ConfigField{
		{Name: "mode", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"rewrite"}, Description: "Rewrite selected system/developer prompts before upstream execution."},
		{Name: "enable_agy", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Apply this filter to selected agy credentials. Default true."},
		{Name: "enable_antigravity", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Apply this filter to selected native antigravity credentials independently. Default true."},
		{Name: "antigravity_bridge_system_prompt", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Native antigravity only: move rewritten system/developer prompts into a USER_REQUEST user message, keeping the native identity as system. Does not affect agy. Default true."},
		{Name: "use_default_keywords", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Use the original keyword preset; with heuristics enabled, names are identity hints rather than broad substitutions."},
		{Name: "custom_mappings", Type: pluginapi.ConfigFieldTypeObject, Description: "Case-insensitive custom mappings; override built-in mappings."},
		{Name: "heuristic_identity", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Conservatively normalize identity and harness declarations to the native Antigravity identity; preserve quoted/code/project content. Default false."},
		{Name: "audit", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Log request ID and before/after body SHA256 only; no prompt contents."},
	}
}

func handleRequestInterceptBefore(request []byte) []byte {
	var req pluginapi.RequestInterceptRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return mustErrorEnvelope("invalid_request", fmt.Sprintf("decode request.intercept_before request: %v", err))
	}
	return mustEnvelope(pluginapi.RequestInterceptResponse{})
}

func handleRequestInterceptAfter(request []byte) []byte {
	var req pluginapi.RequestInterceptRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return mustErrorEnvelope("invalid_request", fmt.Sprintf("decode request.intercept_after request: %v", err))
	}
	// Resolve the actual selected credential, including unprefixed models and aliases.
	// Never infer provider from the OpenAI wire format or caller-controlled model name.
	index, _ := req.Metadata["selected_auth_index"].(string)
	if strings.TrimSpace(index) == "" {
		return mustEnvelope(pluginapi.RequestInterceptResponse{})
	}
	raw, err := hostCall("host.auth.get_runtime", map[string]any{"auth_index": index})
	if err != nil {
		_, _ = hostCall("host.log", map[string]any{"level": "warn", "message": "antigravity-prompt-bridge: provider lookup failed; request unchanged", "fields": map[string]any{"request_id": req.RequestID}})
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
	cfg := activeFilterConfig()
	if !providerFilterEnabled(runtime.Auth.Provider, cfg) {
		return mustEnvelope(pluginapi.RequestInterceptResponse{})
	}
	body, rewritten := rewriteProviderRequestBody(req.Body, cfg, runtime.Auth.Provider)
	if !rewritten {
		return mustEnvelope(pluginapi.RequestInterceptResponse{})
	}
	if cfg.Audit {
		_, _ = hostCall("host.log", map[string]any{"level": "info", "message": "antigravity-prompt-bridge: processed system/developer prompt", "fields": map[string]any{"request_id": req.RequestID, "before_sha256": fmt.Sprintf("%x", sha256.Sum256(req.Body)), "after_sha256": fmt.Sprintf("%x", sha256.Sum256(body)), "native_bridge_enabled": cfg.AntigravityBridgeSystemPrompt && strings.EqualFold(strings.TrimSpace(runtime.Auth.Provider), "antigravity")}})
	}
	return mustEnvelope(pluginapi.RequestInterceptResponse{Body: body})
}

func mustEnvelope(result any) []byte {
	raw, err := json.Marshal(pluginabi.Envelope{OK: true, Result: mustRawMessage(result)})
	if err != nil {
		return mustErrorEnvelope("marshal_error", err.Error())
	}
	return raw
}

func mustErrorEnvelope(code, message string) []byte {
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

var defaultRewriteMappings = []rewriteMapping{
	// Major AI code editors, assistants, and terminal coding agents.
	{Match: "Claude Agent", Replacement: "Antigravity"},
	{Match: "Claude Code", Replacement: "Antigravity"},
	{Match: "OpenAI Codex", Replacement: "Antigravity"},
	{Match: "Codex CLI", Replacement: "Antigravity"},
	{Match: "Codex", Replacement: "Antigravity"},
	{Match: "OpenCode", Replacement: "Antigravity"},
	{Match: "GitHub Copilot CLI", Replacement: "Antigravity"},
	{Match: "GitHub Copilot", Replacement: "Antigravity"},
	{Match: "Gemini Code Assist", Replacement: "Antigravity"},
	{Match: "Gemini CLI", Replacement: "Antigravity"},
	{Match: "Cursor", Replacement: "Antigravity"},
	{Match: "Windsurf", Replacement: "Antigravity"},
	{Match: "Codeium", Replacement: "Antigravity"},
	{Match: "Cline", Replacement: "Antigravity"},
	{Match: "Roo Code", Replacement: "Antigravity"},
	{Match: "Kilo Code", Replacement: "Antigravity"},
	{Match: "Aider", Replacement: "Antigravity"},
	{Match: "Continue.dev", Replacement: "Antigravity"},
	{Match: "Amazon Q Developer", Replacement: "Antigravity"},
	{Match: "Amazon CodeWhisperer", Replacement: "Antigravity"},
	{Match: "JetBrains AI Assistant", Replacement: "Antigravity"},
	{Match: "JetBrains Junie", Replacement: "Antigravity"},
	{Match: "Kiro", Replacement: "Antigravity"},
	{Match: "Qoder CLI", Replacement: "Antigravity"},
	{Match: "Qoder", Replacement: "Antigravity"},
	{Match: "Qwen Code", Replacement: "Antigravity"},
	{Match: "Trae", Replacement: "Antigravity"},
	{Match: "Tabnine", Replacement: "Antigravity"},
	{Match: "Sourcegraph Cody", Replacement: "Antigravity"},
	{Match: "Augment Code", Replacement: "Antigravity"},
	{Match: "Replit Agent", Replacement: "Antigravity"},
	{Match: "Replit Ghostwriter", Replacement: "Antigravity"},
	{Match: "Devin", Replacement: "Antigravity"},
	{Match: "OpenHands", Replacement: "Antigravity"},
	{Match: "SWE-agent", Replacement: "Antigravity"},
	{Match: "Goose", Replacement: "Antigravity"},
	{Match: "Zed AI", Replacement: "Antigravity"},
	{Match: "Void Editor", Replacement: "Antigravity"},
	{Match: "PearAI", Replacement: "Antigravity"},
	{Match: "Refact.ai", Replacement: "Antigravity"},
	{Match: "Tabby", Replacement: "Antigravity"},
	{Match: "GitLab Duo", Replacement: "Antigravity"},
	{Match: "Visual Studio IntelliCode", Replacement: "Antigravity"},
	{Match: "CodeBuddy", Replacement: "Antigravity"},
	{Match: "Blackbox AI", Replacement: "Antigravity"},
	{Match: "Pieces for Developers", Replacement: "Antigravity"},
	{Match: "Qodo", Replacement: "Antigravity"},
	{Match: "CodiumAI", Replacement: "Antigravity"},
	{Match: "Rovo Dev CLI", Replacement: "Antigravity"},
	{Match: "Factory Droid", Replacement: "Antigravity"},

	// General-purpose local agents that can generate and modify code.
	{Match: "OpenClaw", Replacement: "Antigravity"},
	{Match: "Clawdbot", Replacement: "Antigravity"},
	{Match: "Moltbot", Replacement: "Antigravity"},
	{Match: "Hermes Agent", Replacement: "Antigravity"},
	{Match: "Hermes", Replacement: "Antigravity"},
	{Match: "WorkBuddy", Replacement: "Antigravity"},
}

type filterMode string

const (
	filterModeBlock   filterMode = "block"
	filterModeRewrite filterMode = "rewrite"
)

type rewriteMapping struct {
	Match       string
	Replacement string
}

type filterConfig struct {
	Mode                          filterMode
	UseDefaultKeywords            bool
	CustomMappings                []rewriteMapping
	HeuristicIdentity             bool
	EnableAgy                     bool
	EnableAntigravity             bool
	AntigravityBridgeSystemPrompt bool
	Audit                         bool
}

var (
	filterConfigMu      sync.RWMutex
	currentFilterConfig = defaultFilterConfig()
)

func defaultFilterConfig() filterConfig {
	return filterConfig{Mode: filterModeRewrite, UseDefaultKeywords: true, EnableAgy: true, EnableAntigravity: true, AntigravityBridgeSystemPrompt: true}
}

func applyFilterConfig(cfg filterConfig) {
	filterConfigMu.Lock()
	defer filterConfigMu.Unlock()

	currentFilterConfig = filterConfig{
		Mode:                          cfg.Mode,
		Audit:                         cfg.Audit,
		HeuristicIdentity:             cfg.HeuristicIdentity,
		EnableAgy:                     cfg.EnableAgy,
		EnableAntigravity:             cfg.EnableAntigravity,
		AntigravityBridgeSystemPrompt: cfg.AntigravityBridgeSystemPrompt,
		UseDefaultKeywords:            cfg.UseDefaultKeywords,
		CustomMappings:                append([]rewriteMapping(nil), normalizeMappings(cfg.CustomMappings)...),
	}
}

func activeFilterConfig() filterConfig {
	filterConfigMu.RLock()
	defer filterConfigMu.RUnlock()

	return filterConfig{
		Mode:                          currentFilterConfig.Mode,
		Audit:                         currentFilterConfig.Audit,
		HeuristicIdentity:             currentFilterConfig.HeuristicIdentity,
		EnableAgy:                     currentFilterConfig.EnableAgy,
		EnableAntigravity:             currentFilterConfig.EnableAntigravity,
		AntigravityBridgeSystemPrompt: currentFilterConfig.AntigravityBridgeSystemPrompt,
		UseDefaultKeywords:            currentFilterConfig.UseDefaultKeywords,
		CustomMappings:                append([]rewriteMapping(nil), currentFilterConfig.CustomMappings...),
	}
}

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

func filterConfigFromLifecycleRequest(request []byte) (filterConfig, error) {
	var req lifecycleRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return filterConfig{}, fmt.Errorf("decode lifecycle request: %w", err)
	}
	return parseFilterConfigYAML(req.ConfigYAML)
}

func parseFilterConfigYAML(raw []byte) (filterConfig, error) {
	cfg := defaultFilterConfig()
	if len(strings.TrimSpace(string(raw))) == 0 {
		return cfg, nil
	}

	var values map[string]any
	if err := yaml.Unmarshal(raw, &values); err != nil {
		return filterConfig{}, fmt.Errorf("decode config yaml: %w", err)
	}
	if value, exists := values["audit"]; exists {
		v, ok := value.(bool)
		if !ok {
			return filterConfig{}, fmt.Errorf("audit must be a boolean")
		}
		cfg.Audit = v
	}
	if value, exists := values["heuristic_identity"]; exists {
		v, ok := value.(bool)
		if !ok {
			return filterConfig{}, fmt.Errorf("heuristic_identity must be a boolean")
		}
		cfg.HeuristicIdentity = v
	}
	for key, destination := range map[string]*bool{"enable_agy": &cfg.EnableAgy, "enable_antigravity": &cfg.EnableAntigravity, "antigravity_bridge_system_prompt": &cfg.AntigravityBridgeSystemPrompt} {
		if value, exists := values[key]; exists {
			v, ok := value.(bool)
			if !ok {
				return filterConfig{}, fmt.Errorf("%s must be a boolean", key)
			}
			*destination = v
		}
	}
	if value, exists := values["mode"]; exists {
		text, ok := value.(string)
		if !ok {
			return filterConfig{}, fmt.Errorf("mode must be a string")
		}
		cfg.Mode = filterMode(strings.ToLower(strings.TrimSpace(text)))
		if cfg.Mode != filterModeRewrite {
			return filterConfig{}, fmt.Errorf("mode must be rewrite")
		}
	}
	if value, exists := values["use_default_keywords"]; exists {
		boolValue, ok := value.(bool)
		if !ok {
			return filterConfig{}, fmt.Errorf("use_default_keywords must be a boolean")
		}
		cfg.UseDefaultKeywords = boolValue
	}
	if value, exists := values["custom_mappings"]; exists {
		mappings, err := parseCustomMappings(value)
		if err != nil {
			return filterConfig{}, err
		}
		cfg.CustomMappings = mappings
	}
	return cfg, nil
}

func parseCustomMappings(value any) ([]rewriteMapping, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return parseMappingString(typed)
	case map[string]any:
		mappings := make([]rewriteMapping, 0, len(typed))
		for match, replacement := range typed {
			text, ok := replacement.(string)
			if !ok {
				return nil, fmt.Errorf("custom_mappings values must be strings")
			}
			mappings = append(mappings, rewriteMapping{Match: match, Replacement: text})
		}
		return mappings, nil
	case []any:
		mappings := make([]rewriteMapping, 0, len(typed))
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

func parseMappingString(value string) ([]rewriteMapping, error) {
	entries := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	mappings := make([]rewriteMapping, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		match, replacement, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("custom_mappings entries must use match: replacement")
		}
		mappings = append(mappings, rewriteMapping{Match: match, Replacement: replacement})
	}
	return mappings, nil
}

func effectiveMappings(cfg filterConfig) []rewriteMapping {
	mappings := make([]rewriteMapping, 0, len(defaultRewriteMappings)+len(cfg.CustomMappings))
	if cfg.UseDefaultKeywords {
		mappings = append(mappings, defaultRewriteMappings...)
	}
	mappings = append(mappings, cfg.CustomMappings...)
	return normalizeMappings(mappings)
}

func normalizeMappings(mappings []rewriteMapping) []rewriteMapping {
	seen := make(map[string]struct{}, len(mappings))
	reversed := make([]rewriteMapping, 0, len(mappings))
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
		reversed = append(reversed, rewriteMapping{Match: match, Replacement: replacement})
	}
	out := make([]rewriteMapping, 0, len(reversed))
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

type filterDecision struct {
	Blocked bool
	Signal  string
	Detail  string
}

func classifyRequest(body []byte) filterDecision {
	return classifyRequestWithConfig(body, activeFilterConfig())
}

func classifyRequestWithConfig(body []byte, cfg filterConfig) filterDecision {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return filterDecision{}
	}

	mappings := effectiveMappings(cfg)
	var decision filterDecision
	transformEligiblePromptFields(root, func(text string) (string, bool) {
		if decision.Blocked {
			return text, false
		}
		if mapping, protected := findEligibleMapping(text, mappings); protected {
			decision = filterDecision{Blocked: true, Signal: "system.keyword", Detail: mapping.Match}
		}
		return text, false
	})
	return decision
}

func rewriteRequestBody(body []byte) ([]byte, bool) {
	return rewriteRequestBodyWithConfig(body, activeFilterConfig())
}

func rewriteRequestBodyWithConfig(body []byte, cfg filterConfig) ([]byte, bool) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, false
	}
	mappings := effectiveMappings(cfg)
	rewritten, changed := transformEligiblePromptFields(root, func(text string) (string, bool) {
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

// These helpers keep the original classifier/rewrite seams available to
// callers that used the pre-protocol implementation directly. The runtime
// entry points above use the narrower protocol-aware traversal.
func rewriteSystemFields(value any, mappings []rewriteMapping) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		for key, child := range typed {
			var next any
			var childChanged bool
			if key == "system" {
				next, childChanged = rewriteSystemValue(child, mappings)
			} else {
				next, childChanged = rewriteSystemFields(child, mappings)
			}
			if childChanged {
				typed[key] = next
				changed = true
			}
		}
		return typed, changed
	case []any:
		changed := false
		for index, child := range typed {
			next, childChanged := rewriteSystemFields(child, mappings)
			if childChanged {
				typed[index] = next
				changed = true
			}
		}
		return typed, changed
	default:
		return value, false
	}
}

func rewriteSystemValue(value any, mappings []rewriteMapping) (any, bool) {
	switch typed := value.(type) {
	case string:
		return rewritePromptText(typed, mappings)
	case map[string]any:
		changed := false
		for key, child := range typed {
			next, childChanged := rewriteSystemValue(child, mappings)
			if childChanged {
				typed[key] = next
				changed = true
			}
		}
		return typed, changed
	case []any:
		changed := false
		for index, child := range typed {
			next, childChanged := rewriteSystemValue(child, mappings)
			if childChanged {
				typed[index] = next
				changed = true
			}
		}
		return typed, changed
	default:
		return value, false
	}
}

// transformEligiblePromptFields visits only protocol-defined system/developer
// prompt text. It intentionally does not recurse through arbitrary JSON: user
// messages, tool payloads, identifiers, and metadata must remain untouched.
func transformEligiblePromptFields(root any, transform func(string) (string, bool)) (any, bool) {
	return transformPromptObject(root, true, transform)
}

func transformPromptObject(value any, allowPromptFields bool, transform func(string) (string, bool)) (any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return value, false
	}
	changed := false
	if allowPromptFields {
		for _, key := range []string{"system", "instructions"} {
			child, exists := object[key]
			if !exists {
				continue
			}
			next, childChanged := transformPromptTextContainer(child, transform)
			if childChanged {
				object[key] = next
				changed = true
			}
		}
		for _, key := range []string{"input", "messages"} {
			child, exists := object[key]
			if !exists {
				continue
			}
			next, childChanged := transformRoleMessages(child, transform)
			if childChanged {
				object[key] = next
				changed = true
			}
		}
		for _, key := range []string{"systemInstruction", "system_instruction"} {
			child, exists := object[key]
			if !exists {
				continue
			}
			next, childChanged := transformGeminiSystemInstruction(child, transform)
			if childChanged {
				object[key] = next
				changed = true
			}
		}
	}

	// CPA may wrap the original request in an Antigravity `request` object.
	// Re-enter only that protocol wrapper instead of scanning every nested map.
	if child, exists := object["request"]; exists {
		next, childChanged := transformPromptObject(child, true, transform)
		if childChanged {
			object["request"] = next
			changed = true
		}
	}
	return object, changed
}

func transformPromptTextContainer(value any, transform func(string) (string, bool)) (any, bool) {
	switch typed := value.(type) {
	case string:
		return transform(typed)
	case []any:
		changed := false
		for index, child := range typed {
			next, childChanged := transformPromptTextContainer(child, transform)
			if childChanged {
				typed[index] = next
				changed = true
			}
		}
		return typed, changed
	case map[string]any:
		// Anthropic and Responses text blocks carry their prompt in `text`.
		if text, ok := typed["text"].(string); ok {
			next, childChanged := transform(text)
			if childChanged {
				typed["text"] = next
			}
			return typed, childChanged
		}
	}
	return value, false
}

func transformRoleMessages(value any, transform func(string) (string, bool)) (any, bool) {
	switch typed := value.(type) {
	case []any:
		changed := false
		for index, child := range typed {
			next, childChanged := transformRoleMessage(child, transform)
			if childChanged {
				typed[index] = next
				changed = true
			}
		}
		return typed, changed
	case map[string]any:
		return transformRoleMessage(typed, transform)
	default:
		return value, false
	}
}

func transformRoleMessage(value any, transform func(string) (string, bool)) (any, bool) {
	message, ok := value.(map[string]any)
	if !ok {
		return value, false
	}
	role, _ := message["role"].(string)
	role = strings.ToLower(strings.TrimSpace(role))
	if role != "system" && role != "developer" {
		return value, false
	}
	content, exists := message["content"]
	if !exists {
		return value, false
	}
	next, changed := transformMessageContent(content, transform)
	if changed {
		message["content"] = next
	}
	return message, changed
}

func transformMessageContent(value any, transform func(string) (string, bool)) (any, bool) {
	switch typed := value.(type) {
	case string:
		return transform(typed)
	case []any:
		changed := false
		for index, child := range typed {
			block, ok := child.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			blockType = strings.ToLower(strings.TrimSpace(blockType))
			if blockType != "text" && blockType != "input_text" {
				continue
			}
			text, ok := block["text"].(string)
			if !ok {
				continue
			}
			next, childChanged := transform(text)
			if childChanged {
				block["text"] = next
				typed[index] = block
				changed = true
			}
		}
		return typed, changed
	case map[string]any:
		blockType, _ := typed["type"].(string)
		blockType = strings.ToLower(strings.TrimSpace(blockType))
		if blockType != "text" && blockType != "input_text" {
			return value, false
		}
		text, ok := typed["text"].(string)
		if !ok {
			return value, false
		}
		next, changed := transform(text)
		if changed {
			typed["text"] = next
		}
		return typed, changed
	default:
		return value, false
	}
}

func transformGeminiSystemInstruction(value any, transform func(string) (string, bool)) (any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return value, false
	}
	parts, ok := object["parts"].([]any)
	if !ok {
		return value, false
	}
	changed := false
	for index, part := range parts {
		partObject, ok := part.(map[string]any)
		if !ok {
			continue
		}
		text, ok := partObject["text"].(string)
		if !ok {
			continue
		}
		next, partChanged := transform(text)
		if partChanged {
			partObject["text"] = next
			parts[index] = partObject
			changed = true
		}
	}
	return object, changed
}

func findEligibleMapping(text string, mappings []rewriteMapping) (rewriteMapping, bool) {
	for _, mapping := range mappings {
		if containsUnprotected(text, mapping.Match) {
			return mapping, true
		}
	}
	return rewriteMapping{}, false
}

func rewritePromptText(value string, mappings []rewriteMapping) (string, bool) {
	next := value
	changed := false
	for _, mapping := range mappings {
		var replaced bool
		next, replaced = replaceInsensitiveProtected(next, mapping.Match, mapping.Replacement)
		changed = changed || replaced
	}
	return next, changed
}

func containsUnprotected(value, match string) bool {
	lowerValue := strings.ToLower(value)
	lowerMatch := strings.ToLower(match)
	if lowerMatch == "" {
		return false
	}
	protected := protectedTextRanges(value)
	for start := 0; ; {
		index := strings.Index(lowerValue[start:], lowerMatch)
		if index < 0 {
			return false
		}
		index += start
		if !overlapsProtected(index, index+len(match), protected) {
			return true
		}
		start = index + len(match)
		if start > len(value) {
			return false
		}
	}
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

func replaceInsensitive(value, match, replacement string) (string, bool) {
	return replaceInsensitiveProtected(value, match, replacement)
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

func walkJSON(value any, visit func(path []string, value any) bool) {
	var walk func(path []string, current any) bool
	walk = func(path []string, current any) bool {
		if !visit(path, current) {
			return false
		}
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if !walk(appendPath(path, key), child) {
					return false
				}
			}
		case []any:
			for index, child := range typed {
				if !walk(appendPath(path, fmt.Sprintf("%d", index)), child) {
					return false
				}
			}
		}
		return true
	}
	walk(nil, value)
}

func appendPath(path []string, item string) []string {
	next := make([]string, len(path), len(path)+1)
	copy(next, path)
	return append(next, item)
}

func collectText(value any) string {
	var parts []string
	var collect func(any)
	collect = func(current any) {
		switch typed := current.(type) {
		case string:
			parts = append(parts, typed)
		case map[string]any:
			for _, child := range typed {
				collect(child)
			}
		case []any:
			for _, child := range typed {
				collect(child)
			}
		}
	}
	collect(value)
	return strings.Join(parts, "\n")
}

var hostCall = callHost

func callHost(method string, payload any) (json.RawMessage, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal host callback %s: %w", method, err)
	}

	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var response C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(rawPayload) > 0 {
		cPayload := C.CBytes(rawPayload)
		if cPayload == nil {
			return nil, fmt.Errorf("allocate host callback %s", method)
		}
		defer C.free(cPayload)
		requestPtr = (*C.uint8_t)(cPayload)
	}

	callCode := C.call_host_api(cMethod, requestPtr, C.size_t(len(rawPayload)), &response)
	var rawResponse []byte
	if response.ptr != nil && response.len > 0 {
		rawResponse = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len)
	}
	if len(rawResponse) == 0 {
		return nil, fmt.Errorf("host callback %s returned no response, code=%d", method, int(callCode))
	}

	var env pluginabi.Envelope
	if err := json.Unmarshal(rawResponse, &env); err != nil {
		return nil, fmt.Errorf("decode host callback %s: %w", method, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	if callCode != 0 {
		return nil, fmt.Errorf("host callback %s returned code=%d", method, int(callCode))
	}
	return append(json.RawMessage(nil), env.Result...), nil
}
