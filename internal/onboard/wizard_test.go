package onboard

import (
	"reflect"
	"sort"
	"testing"

	"linker/internal/config"
	"linker/internal/provider"
	"linker/internal/state"
)

func TestMappingChoicesIncludesBuiltinAndDiscoveredModels(t *testing.T) {
	registry := provider.NewRegistry()
	wizard := &Wizard{providers: registry}

	cfg := config.Default("/tmp/claude-settings.json")
	cfg.Providers = map[string]config.ProviderConfig{
		"codex": {
			Type:            "oauth",
			Enabled:         true,
			Accounts:        []config.AccountRef{{ID: "acct-1", Email: "user@example.com", Active: true, AuthFile: "auth/codex_user.json"}},
			ActiveAccountID: "acct-1",
		},
		"gemini-apikey": {
			Type:             "apikey-pool",
			Enabled:          true,
			AuthFile:         "auth/gemini.json",
			RotationStrategy: "round-robin",
		},
		"gemini-cli": {
			Type:    "oauth",
			Enabled: false,
		},
	}

	modelRegistry := state.ModelRegistry{
		Entries: map[string][]state.DiscoveredModel{
			"codex:acct-1": {
				{Name: builtinModelNames(registry, "codex")[0]},
				{Name: "custom-codex-model"},
			},
			"gemini-apikey": {
				{Name: builtinModelNames(registry, "gemini-apikey")[0]},
				{Name: "gemini-3.0-flash"},
			},
		},
	}

	providerOrder, modelsByProvider := wizard.mappingChoices(cfg, modelRegistry)

	if want := []string{"codex", "gemini-apikey"}; !reflect.DeepEqual(providerOrder, want) {
		t.Fatalf("providerOrder = %v, want %v", providerOrder, want)
	}

	wantCodex := append(builtinModelNames(registry, "codex"), "custom-codex-model")
	sort.Strings(wantCodex)
	if !reflect.DeepEqual(modelsByProvider["codex"], uniqueSorted(wantCodex)) {
		t.Fatalf("codex models = %v, want %v", modelsByProvider["codex"], uniqueSorted(wantCodex))
	}

	wantGemini := append(builtinModelNames(registry, "gemini-apikey"), "gemini-3.0-flash")
	sort.Strings(wantGemini)
	if !reflect.DeepEqual(modelsByProvider["gemini-apikey"], uniqueSorted(wantGemini)) {
		t.Fatalf("gemini-apikey models = %v, want %v", modelsByProvider["gemini-apikey"], uniqueSorted(wantGemini))
	}

	if _, ok := modelsByProvider["gemini-cli"]; ok {
		t.Fatalf("disabled provider should not be included in mapping choices")
	}
}

func TestOptionIndexUsesCurrentSelectionWhenAvailable(t *testing.T) {
	options := []string{"codex", "gemini-apikey", "gemini-cli"}

	if got := optionIndex(options, "gemini-apikey"); got != 1 {
		t.Fatalf("optionIndex returned %d, want 1", got)
	}
	if got := optionIndex(options, "missing"); got != 0 {
		t.Fatalf("optionIndex fallback returned %d, want 0", got)
	}
}

func builtinModelNames(registry *provider.Registry, providerID string) []string {
	models := registry.BuiltinModels(providerID)
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.Name)
	}
	return names
}

func uniqueSorted(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if contains(result, value) {
			continue
		}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
