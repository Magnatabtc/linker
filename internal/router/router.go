package router

import (
	"errors"
	"fmt"
	"strings"

	"linker/internal/config"
	"linker/internal/provider"
	"linker/internal/state"
)

type Target struct {
	Provider  string
	Model     string
	AccountID string
	AuthFile  string
}

func Resolve(cfg config.Config, registry state.ModelRegistry, providers *provider.Registry, requested string) (Target, error) {
	targets, err := ResolveCandidates(cfg, registry, providers, requested)
	if err != nil {
		return Target{}, err
	}
	return targets[0], nil
}

func ResolveCandidates(cfg config.Config, registry state.ModelRegistry, providers *provider.Registry, requested string) ([]Target, error) {
	if target, ok := resolveSlot(cfg, requested); ok {
		return resolveAccounts(cfg, target)
	}

	targets := []Target{}
	for providerID, providerCfg := range cfg.Providers {
		if !providerCfg.Enabled {
			continue
		}
		if providerCfg.UsesProviderAuth() {
			for _, model := range registry.Entries[providerID] {
				if strings.EqualFold(model.Name, requested) {
					targets = append(targets, Target{
						Provider: providerID,
						Model:    model.Name,
						AuthFile: providerCfg.AuthFile,
					})
				}
			}
			for _, model := range providers.BuiltinModels(providerID) {
				if strings.EqualFold(model.Name, requested) {
					targets = append(targets, Target{
						Provider: providerID,
						Model:    model.Name,
						AuthFile: providerCfg.AuthFile,
					})
				}
			}
			continue
		}
		for _, account := range orderedAccounts(providerCfg) {
			key := providerID + ":" + account.ID
			for _, model := range registry.Entries[key] {
				if strings.EqualFold(model.Name, requested) {
					targets = append(targets, Target{
						Provider:  providerID,
						Model:     model.Name,
						AccountID: account.ID,
						AuthFile:  account.AuthFile,
					})
				}
			}
			for _, model := range providers.BuiltinModels(providerID) {
				if strings.EqualFold(model.Name, requested) {
					targets = append(targets, Target{
						Provider:  providerID,
						Model:     model.Name,
						AccountID: account.ID,
						AuthFile:  account.AuthFile,
					})
				}
			}
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no route found for model %q", requested)
	}
	return targets, nil
}

func resolveSlot(cfg config.Config, requested string) (config.ModelTarget, bool) {
	lower := strings.ToLower(requested)
	switch {
	case lower == "" && cfg.ModelMapping.Default.Model != "":
		return cfg.ModelMapping.Default, true
	case strings.Contains(lower, "claude-opus"):
		return cfg.ModelMapping.Opus, true
	case strings.Contains(lower, "claude-sonnet"):
		return cfg.ModelMapping.Sonnet, true
	case strings.Contains(lower, "claude-haiku"):
		return cfg.ModelMapping.Haiku, true
	case strings.EqualFold(requested, cfg.ModelMapping.Default.Model):
		return cfg.ModelMapping.Default, true
	case strings.EqualFold(requested, cfg.ModelMapping.Opus.Model):
		return cfg.ModelMapping.Opus, true
	case strings.EqualFold(requested, cfg.ModelMapping.Sonnet.Model):
		return cfg.ModelMapping.Sonnet, true
	case strings.EqualFold(requested, cfg.ModelMapping.Haiku.Model):
		return cfg.ModelMapping.Haiku, true
	default:
		return config.ModelTarget{}, false
	}
}

func resolveAccounts(cfg config.Config, target config.ModelTarget) ([]Target, error) {
	if target.Provider == "" || target.Model == "" {
		return nil, errors.New("model mapping is incomplete")
	}
	providerCfg, ok := cfg.Providers[target.Provider]
	if !ok || !providerCfg.Enabled {
		return nil, fmt.Errorf("provider %q is not enabled", target.Provider)
	}
	if providerCfg.UsesProviderAuth() {
		if strings.TrimSpace(providerCfg.AuthFile) == "" {
			return nil, fmt.Errorf("provider %q has no auth file", target.Provider)
		}
		return []Target{{
			Provider: target.Provider,
			Model:    target.Model,
			AuthFile: providerCfg.AuthFile,
		}}, nil
	}
	targets := []Target{}
	for _, account := range orderedAccounts(providerCfg) {
		targets = append(targets, Target{
			Provider:  target.Provider,
			Model:     target.Model,
			AccountID: account.ID,
			AuthFile:  account.AuthFile,
		})
	}
	if len(targets) > 0 {
		return targets, nil
	}
	return nil, fmt.Errorf("provider %q has no accounts", target.Provider)
}

func orderedAccounts(providerCfg config.ProviderConfig) []config.AccountRef {
	if len(providerCfg.Accounts) == 0 {
		return nil
	}
	index := 0
	for i, account := range providerCfg.Accounts {
		if providerCfg.ActiveAccountID != "" && account.ID == providerCfg.ActiveAccountID {
			index = i
			break
		}
		if providerCfg.ActiveAccountID == "" && account.Active {
			index = i
			break
		}
	}
	ordered := make([]config.AccountRef, 0, len(providerCfg.Accounts))
	ordered = append(ordered, providerCfg.Accounts[index:]...)
	ordered = append(ordered, providerCfg.Accounts[:index]...)
	return ordered
}
