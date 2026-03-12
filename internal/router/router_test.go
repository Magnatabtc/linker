package router

import (
	"testing"

	"linker/internal/config"
	"linker/internal/provider"
	"linker/internal/state"
)

func TestResolveSlotRoute(t *testing.T) {
	t.Parallel()

	cfg := config.Default("settings.json")
	cfg.Providers["antigravity"] = config.ProviderConfig{
		Enabled: true,
		Accounts: []config.AccountRef{{
			ID:       "acc1",
			Email:    "user@example.com",
			Active:   true,
			AuthFile: "auth/acc1.json",
		}},
		ActiveAccountID: "acc1",
	}
	cfg.ModelMapping.Opus = config.ModelTarget{Provider: "antigravity", Model: "gpt-5.4"}

	target, err := Resolve(cfg, state.ModelRegistry{Entries: map[string][]state.DiscoveredModel{}}, provider.NewRegistry(), "claude-opus-4-20250514")
	if err != nil {
		t.Fatalf("resolve route: %v", err)
	}
	if target.Provider != "antigravity" || target.Model != "gpt-5.4" || target.AccountID != "acc1" {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestResolveProviderScopedRoute(t *testing.T) {
	t.Parallel()

	cfg := config.Default("settings.json")
	cfg.Providers["gemini-apikey"] = config.ProviderConfig{
		Type:             "apikey-pool",
		Enabled:          true,
		AuthFile:         "auth/gemini-apikey.json",
		RotationStrategy: "round-robin",
	}
	cfg.ModelMapping.Sonnet = config.ModelTarget{Provider: "gemini-apikey", Model: "gemini-2.5-pro"}

	target, err := Resolve(cfg, state.ModelRegistry{
		Entries: map[string][]state.DiscoveredModel{
			"gemini-apikey": {{
				Provider:  "gemini-apikey",
				AccountID: "gemini-apikey",
				Name:      "gemini-2.5-pro",
				Label:     "gemini-2.5-pro [Gemini API Key pool: 2 keys]",
			}},
		},
	}, provider.NewRegistry(), "claude-sonnet-4-20250514")
	if err != nil {
		t.Fatalf("resolve provider-scoped route: %v", err)
	}
	if target.Provider != "gemini-apikey" || target.Model != "gemini-2.5-pro" || target.AuthFile != "auth/gemini-apikey.json" {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestResolveCandidatesOrdersAccountsFromActiveCursor(t *testing.T) {
	t.Parallel()

	cfg := config.Default("settings.json")
	cfg.Providers["gemini-cli"] = config.ProviderConfig{
		Enabled: true,
		Accounts: []config.AccountRef{
			{ID: "acc1", Email: "one@example.com", Active: false, AuthFile: "auth/gemini-cli_one@example.com.json"},
			{ID: "acc2", Email: "two@example.com", Active: true, AuthFile: "auth/gemini-cli_two@example.com.json"},
			{ID: "acc3", Email: "three@example.com", Active: false, AuthFile: "auth/gemini-cli_three@example.com.json"},
		},
		ActiveAccountID: "acc2",
	}
	cfg.ModelMapping.Sonnet = config.ModelTarget{Provider: "gemini-cli", Model: "gemini-2.5-pro"}

	targets, err := ResolveCandidates(cfg, state.ModelRegistry{}, provider.NewRegistry(), "claude-sonnet-4-20250514")
	if err != nil {
		t.Fatalf("resolve candidates: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(targets))
	}
	if targets[0].AccountID != "acc2" || targets[1].AccountID != "acc3" || targets[2].AccountID != "acc1" {
		t.Fatalf("unexpected candidate order: %#v", targets)
	}
}
