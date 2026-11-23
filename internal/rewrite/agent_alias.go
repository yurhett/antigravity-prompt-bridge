package rewrite

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

const maxAliasStreamBytes = 16 << 20

type AliasState struct {
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

func aliasToolKey(namespace, name string) string { return namespace + "\x00" + name }
func (s *AliasState) restoreFunction(function map[string]any) error {
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
				if err := plan.RestoreCall(protocol.AgentToolCall{Object: function, ArgumentKey: key}); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

func (s *AliasState) restoreResponse(value any) error {
	return protocol.VisitResponseCalls(value, s.restoreFunction)
}

func (s *AliasState) RestoreResponse(body []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := protocol.DecodeJSON(body)
	if err != nil {
		return nil, err
	}
	if err := s.restoreResponse(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
