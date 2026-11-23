package plugin

import (
	"testing"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
)

func TestAgentUnifiedSwitch(t *testing.T) {
	cfg, err := config.Parse([]byte("agent_alias: false\n"))
	if err != nil || cfg.AgentAlias {
		t.Fatal(cfg, err)
	}
	for _, field := range configFields() {
		if field.Name == "opencode_alias" {
			t.Fatal("retired config exposed")
		}
	}
}
func TestDefaultFilterModeRewrites(t *testing.T) {
	cfg, err := config.Parse(nil)
	if err != nil {
		t.Fatalf("parse default config: %v", err)
	}
	if cfg.Mode != config.ModeRewrite {
		t.Fatalf("default mode = %q, want rewrite", cfg.Mode)
	}

	cfg, err = config.Parse([]byte("mode: rewrite\n"))
	if err != nil {
		t.Fatalf("parse rewrite config: %v", err)
	}
	if cfg.Mode != config.ModeRewrite {
		t.Fatalf("configured mode = %q, want rewrite", cfg.Mode)
	}
}
