package rewrite

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
)

func BenchmarkAliasEngine(b *testing.B) {
	for _, size := range []int{8 << 10, 64 << 10, 256 << 10} {
		body, _ := json.Marshal(map[string]any{"system": benchmarkPrompt(size), "tools": []any{map[string]any{"name": "read", "description": "Read files with OpenCode.", "parameters": map[string]any{"type": "object"}}}, "contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "OpenCode user text"}}}}})
		for _, enabled := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/enabled=%v", size, enabled), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for i := 0; i < b.N; i++ {
					if enabled {
						if _, _, err := aliasAgentRequest(body); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}

func benchmarkPrompt(size int) string {
	const prefix = "You are OpenCode.\n"
	const line = "Follow project rules and preserve tool protocols.\n"
	return prefix + strings.Repeat(line, size/len(line)+1)[:size-len(prefix)]
}

func BenchmarkAdapterPipeline(b *testing.B) {
	adapters := registeredAgentAdapters
	defer func() { registeredAgentAdapters = adapters }()
	common := append([]agentRegistration(nil), adapters...)
	for i := range common {
		common[i].execution = nil
	}
	for _, size := range []int{8 << 10, 64 << 10, 256 << 10} {
		root := codeModeRoot()
		root["system"] = codeModeSystem + benchmarkPrompt(size-len(codeModeSystem))
		root["contents"] = []any{map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "execute", "id": "benchmark-call", "args": map[string]any{"code": `const r=await search({namespace:"opencode",limit:1}); return {found:r.items.length};`}}}}}}
		body, _ := json.Marshal(root)

		for _, mode := range []string{"off", "common", "opencode"} {
			b.Run(fmt.Sprintf("%d/%s", size, mode), func(b *testing.B) {
				cfg := config.Default()
				cfg.HeuristicIdentity = true
				cfg.AntigravityBridgeSystemPrompt = false
				cfg.AgentAlias = mode != "off"
				registeredAgentAdapters = adapters
				if mode == "common" {
					registeredAgentAdapters = common
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := Request(body, cfg, "gemini"); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
