package plugin

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/yurhett/antigravity-prompt-bridge/internal/rewrite"
)

const maxAliasRequests = 4096

type aliasStateStore struct {
	mu     sync.Mutex
	states map[string]*rewrite.AliasState
}

func (s *aliasStateStore) put(id string, state *rewrite.AliasState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.states[id]; !ok && len(s.states) >= maxAliasRequests {
		return fmt.Errorf("too many active alias requests")
	}
	s.states[id] = state
	return nil
}
func (s *aliasStateStore) get(id string) *rewrite.AliasState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[id]
}
func (s *aliasStateStore) remove(id string) { s.mu.Lock(); defer s.mu.Unlock(); delete(s.states, id) }
func (s *aliasStateStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = map[string]*rewrite.AliasState{}
}

func aliasErrorBody() []byte {
	return []byte(`{"type":"error","error":{"type":"invalid_response_error","code":"agent_alias_restore_failed","message":"Agent tool names could not be restored safely"}}`)
}
func (p *Plugin) handleAliasResponse(request []byte) []byte {
	var req pluginapi.ResponseInterceptRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return ErrorEnvelope("invalid_request", err.Error())
	}
	state := p.aliases.get(req.RequestID)
	if state == nil {
		return mustEnvelope(pluginapi.ResponseInterceptResponse{})
	}
	body, err := state.RestoreResponse(req.Body)
	if err != nil {
		body = aliasErrorBody()
	}
	return mustEnvelope(pluginapi.ResponseInterceptResponse{Body: body})
}

func (p *Plugin) handleAliasStream(request []byte) []byte {
	var req pluginapi.StreamChunkInterceptRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return ErrorEnvelope("invalid_request", err.Error())
	}
	state := p.aliases.get(req.RequestID)
	if state == nil || req.ChunkIndex < 0 || len(req.Body) == 0 {
		return mustEnvelope(pluginapi.StreamChunkInterceptResponse{})
	}
	body, drop, err := state.RestoreChunk(req.Body)
	if err != nil {
		body = append(append([]byte("event: error\ndata: "), aliasErrorBody()...), []byte("\n\n")...)
	}
	return mustEnvelope(pluginapi.StreamChunkInterceptResponse{Body: body, DropChunk: drop})
}
