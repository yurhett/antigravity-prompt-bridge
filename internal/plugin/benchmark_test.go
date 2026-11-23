package plugin

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
)

// BenchmarkProviderPipeline includes request decoding, identity processing,
// host provider lookup (mocked), alias state registration and cleanup. It does
// not include CPA, network, or model inference time.
func BenchmarkProviderPipeline(b *testing.B) {
	p := New(func(string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"auth":{"provider":"antigravity"}}`), nil
	})
	for _, size := range []int{8 << 10, 64 << 10, 256 << 10} {
		body, _ := json.Marshal(map[string]any{
			"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": benchmarkPrompt(size)}}},
			"tools":             []any{map[string]any{"functionDeclarations": []any{map[string]any{"name": "read", "description": "Read files with OpenCode.", "parameters": map[string]any{"type": "object"}}}}},
			"contents":          []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "OpenCode user text"}}}},
		})
		raw, _ := json.Marshal(pluginapi.RequestInterceptRequest{RequestID: "benchmark", SourceFormat: "gemini", Body: body, Metadata: map[string]any{"selected_auth_index": "mock"}})
		for _, enabled := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/enabled=%v", size, enabled), func(b *testing.B) {
				cfg := config.Default()
				cfg.HeuristicIdentity = true
				cfg.AntigravityBridgeSystemPrompt = false
				cfg.AgentAlias = enabled
				p.setConfig(cfg)
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchmarkAliasSink = p.handleRequestAfter(raw)
					p.aliases.remove("benchmark")
				}
			})
		}
	}
}

var benchmarkAliasSink []byte

func benchmarkPrompt(size int) string {
	const prefix = "You are OpenCode.\n"
	const line = "Follow project rules and preserve tool protocols.\n"
	return prefix + strings.Repeat(line, size/len(line)+1)[:size-len(prefix)]
}
