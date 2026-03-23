package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	store := NewStore(path)
	base := Default(filepath.Join(t.TempDir(), "settings.json"))
	base.Port = 7878
	base.Bind = "0.0.0.0"
	base.LogLevel = "debug"

	if err := store.Save(base); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got, err := store.Load(Default(""))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got.Port != 7878 || got.Bind != "0.0.0.0" || got.LogLevel != "debug" {
		t.Fatalf("unexpected config: %#v", got)
	}
}

func TestStoreSetAndGet(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	store := NewStore(path)
	cfg := Default("settings.json")
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if _, err := store.Set(cfg, "port", "7001"); err != nil {
		t.Fatalf("set port: %v", err)
	}
	value, err := store.Get(cfg, "port")
	if err != nil {
		t.Fatalf("get port: %v", err)
	}
	if value != "7001" {
		t.Fatalf("expected 7001, got %s", value)
	}
}

func TestLoadNormalizesProviderTypes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	store := NewStore(path)
	cfg := Default("settings.json")
	cfg.Providers["gemini-apikey"] = ProviderConfig{
		Enabled:  true,
		AuthFile: "auth/gemini-apikey.json",
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got, err := store.Load(Default("settings.json"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got.Providers["gemini-apikey"].Type != "apikey-pool" {
		t.Fatalf("expected apikey-pool, got %#v", got.Providers["gemini-apikey"])
	}
}

func TestLoadNormalizesLegacyCodexModelMapping(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	store := NewStore(path)
	legacy := Default("settings.json")
	legacy.ModelMapping.Default = ModelTarget{Provider: "codex", Model: "gpt-5-codex"}
	legacy.ModelMapping.Opus = ModelTarget{Provider: "codex", Model: "gpt-5-mini-codex"}
	legacy.ModelMapping.Sonnet = ModelTarget{Provider: "codex", Model: "gpt-5-codex"}
	legacy.ModelMapping.Haiku = ModelTarget{Provider: "codex", Model: "gpt-5-mini-codex"}

	payload, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy config: %v", err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	got, err := store.Load(Default("settings.json"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	for name, target := range map[string]ModelTarget{
		"default": got.ModelMapping.Default,
		"opus":    got.ModelMapping.Opus,
		"sonnet":  got.ModelMapping.Sonnet,
		"haiku":   got.ModelMapping.Haiku,
	} {
		wantModel := "gpt-5.4"
		if name == "opus" || name == "haiku" {
			wantModel = "gpt-5.4-mini"
		}
		if target.Provider != "codex" || target.Model != wantModel {
			t.Fatalf("%s mapping = %#v, want codex/%s", name, target, wantModel)
		}
	}
}

func TestSaveNormalizesLegacyCodexModelMapping(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	store := NewStore(path)
	cfg := Default("settings.json")
	cfg.ModelMapping.Default = ModelTarget{Provider: "codex", Model: "gpt-5-codex"}
	cfg.ModelMapping.Opus = ModelTarget{Provider: "codex", Model: "gpt-5-mini-codex"}
	cfg.ModelMapping.Sonnet = ModelTarget{Provider: "codex", Model: "gpt-5-codex"}
	cfg.ModelMapping.Haiku = ModelTarget{Provider: "codex", Model: "gpt-5-mini-codex"}

	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	var got Config
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode saved config: %v", err)
	}
	for name, target := range map[string]ModelTarget{
		"default": got.ModelMapping.Default,
		"opus":    got.ModelMapping.Opus,
		"sonnet":  got.ModelMapping.Sonnet,
		"haiku":   got.ModelMapping.Haiku,
	} {
		wantModel := "gpt-5.4"
		if name == "opus" || name == "haiku" {
			wantModel = "gpt-5.4-mini"
		}
		if target.Provider != "codex" || target.Model != wantModel {
			t.Fatalf("%s mapping = %#v, want codex/%s", name, target, wantModel)
		}
	}
}
