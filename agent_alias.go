package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const maxAliasRequests = 4096
const maxAliasStreamBytes = 16 << 20

type agentAliasState struct {
	mu           sync.Mutex
	functions    map[string]string // Native namespace + NUL + upstream name -> client name.
	namespaces   map[string]string
	adapters     []AgentAdapterPlan
	renamed      bool
	stream       []byte
	failed       bool
	calls        map[string]*streamAliasCall
	pendingBytes int
	sequence     int
}

var agentAliases = aliasStateStore{states: map[string]*agentAliasState{}}

type aliasStateStore struct {
	mu     sync.Mutex
	states map[string]*agentAliasState
}

func (s *aliasStateStore) put(id string, state *agentAliasState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.states[id]; !ok && len(s.states) >= maxAliasRequests {
		return fmt.Errorf("too many active alias requests")
	}
	s.states[id] = state
	return nil
}
func (s *aliasStateStore) get(id string) *agentAliasState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[id]
}
func (s *aliasStateStore) remove(id string) { s.mu.Lock(); defer s.mu.Unlock(); delete(s.states, id) }
func (s *aliasStateStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = map[string]*agentAliasState{}
}

func decodeAliasJSON(body []byte) (any, error) {
	var value any
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	return value, nil
}

func aliasToolKey(namespace, name string) string { return namespace + "\x00" + name }
func (s *agentAliasState) restoreFunction(function map[string]any) error {
	name, _ := function["name"].(string)
	namespace, _ := function["namespace"].(string)
	if name == "" {
		return nil
	}
	lookupNS := namespace
	for upstream, original := range s.namespaces {
		if namespace == original {
			lookupNS = upstream
			break
		}
	}
	original, known := s.functions[aliasToolKey(lookupNS, name)]
	if !known {
		for key, client := range s.functions {
			if client == name && strings.HasPrefix(key, lookupNS+"\x00") {
				original, known = name, true
				break
			}
		}
	}
	if !known && s.renamed {
		return fmt.Errorf("unrecognized upstream tool name")
	}
	if known {
		function["name"] = original
	}
	if ns, ok := s.namespaces[namespace]; ok {
		function["namespace"] = ns
	}
	for _, plan := range s.adapters {
		for _, key := range []string{"args", "arguments", "input"} {
			if _, exists := function[key]; exists {
				if err := plan.RestoreCall(AgentToolCall{Object: function, ArgumentKey: key}); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// Only native protocol name slots are eligible. Never descend into arguments,
// result payloads, schemas, text, signatures, encrypted content, or media.
func (s *agentAliasState) restoreResponse(value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	restoreBlock := func(v any) error {
		b, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		if b["type"] == "function_call" || b["type"] == "custom_tool_call" || b["type"] == "tool_use" {
			return s.restoreFunction(b)
		}
		return nil
	}
	if err := restoreBlock(object); err != nil {
		return err
	}
	if response, ok := object["response"].(map[string]any); ok {
		if err := s.restoreResponse(response); err != nil {
			return err
		}
	}
	for _, key := range []string{"item", "content_block"} {
		if err := restoreBlock(object[key]); err != nil {
			return err
		}
	}
	for _, key := range []string{"output", "content"} {
		if blocks, ok := object[key].([]any); ok {
			for _, b := range blocks {
				if err := restoreBlock(b); err != nil {
					return err
				}
			}
		}
	}
	if candidates, ok := object["candidates"].([]any); ok {
		for _, v := range candidates {
			c, _ := v.(map[string]any)
			content, _ := c["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, v := range parts {
				p, _ := v.(map[string]any)
				for _, field := range []string{"functionCall", "function_call"} {
					if f, ok := p[field].(map[string]any); ok {
						if err := s.restoreFunction(f); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	if choices, ok := object["choices"].([]any); ok {
		for _, v := range choices {
			c, _ := v.(map[string]any)
			for _, key := range []string{"message", "delta"} {
				m, _ := c[key].(map[string]any)
				if f, ok := m["function_call"].(map[string]any); ok {
					if err := s.restoreFunction(f); err != nil {
						return err
					}
				}
				calls, _ := m["tool_calls"].([]any)
				for _, v := range calls {
					t, _ := v.(map[string]any)
					if f, ok := t["function"].(map[string]any); ok {
						if err := s.restoreFunction(f); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

func aliasErrorBody() []byte {
	return []byte(`{"type":"error","error":{"type":"invalid_response_error","code":"agent_alias_restore_failed","message":"Agent tool names could not be restored safely"}}`)
}
func handleAliasResponse(request []byte) []byte {
	var req pluginapi.ResponseInterceptRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return mustErrorEnvelope("invalid_request", err.Error())
	}
	state := agentAliases.get(req.RequestID)
	if state == nil {
		return mustEnvelope(pluginapi.ResponseInterceptResponse{})
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	value, err := decodeAliasJSON(req.Body)
	if err == nil {
		err = state.restoreResponse(value)
	}
	if err != nil {
		return mustEnvelope(pluginapi.ResponseInterceptResponse{Body: aliasErrorBody()})
	}
	raw, _ := json.Marshal(value)
	return mustEnvelope(pluginapi.ResponseInterceptResponse{Body: raw})
}
