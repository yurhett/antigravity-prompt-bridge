package rewrite

import (
	"sync"
	"testing"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

func aliasAgentRequest(body []byte) ([]byte, *AliasState, error) {
	result, err := rewriteAgentRequest(body, config.Default())
	return result.Body, result.State, err
}

func TestAgentAliasIgnoresUnchangedAndMalformedBodies(t *testing.T) {
	for _, body := range []string{`{"text":"Antigravity"}`, `{`, `{"text":"opencode"} trailing`} {
		if _, state, err := aliasAgentRequest([]byte(body)); state != nil || err != nil {
			t.Errorf("unexpected alias result for %s: %v %v", body, state, err)
		}
	}
}

func TestAgentAliasStateIsolationConcurrent(t *testing.T) {
	var workers sync.WaitGroup
	for i := 0; i < 24; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, state, err := aliasAgentRequest([]byte(`{"system":"You are OpenCode.","tools":[{"name":"opencode_probe"}]}`))
			if err != nil {
				t.Error(err)
				return
			}
			value, _ := protocol.DecodeJSON([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"antigravity_probe","args":{"namespace":"antigravity"}}}]}}]}`))
			state.restoreResponse(value)
		}()
	}
	workers.Wait()
}
