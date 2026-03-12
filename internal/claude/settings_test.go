package claude

import (
	"path/filepath"
	"testing"

	"linker/internal/config"
)

func TestMergePreservesExistingKeys(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")
	if err := Save(path, map[string]any{
		"theme": "dark",
		"env": map[string]any{
			"EXISTING": "1",
		},
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	cfg := config.Default(path)
	cfg.Port = 6767
	cfg.Bind = "127.0.0.1"
	cfg.ModelMapping.Opus.Model = "gpt-5.4"
	cfg.ModelMapping.Sonnet.Model = "gemini-2.5-pro"
	cfg.ModelMapping.Haiku.Model = "qwen3-coder"

	merged, err := Merge(path, DesiredEnv(cfg))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	env := merged["env"].(map[string]any)
	if env["EXISTING"] != "1" {
		t.Fatalf("expected existing env key to be preserved")
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "linker-local" {
		t.Fatalf("expected linker-local token, got %#v", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if _, ok := env["CLAUDE_CODE_ATTRIBUTION_HEADER"]; ok {
		t.Fatalf("did not expect CLAUDE_CODE_ATTRIBUTION_HEADER in managed env")
	}
	if merged["theme"] != "dark" {
		t.Fatalf("expected theme preserved")
	}
}
