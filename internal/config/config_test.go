package config

import (
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
