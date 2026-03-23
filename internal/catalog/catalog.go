package catalog

import (
	"context"
	"time"

	"linker/internal/config"
	"linker/internal/provider"
	"linker/internal/state"
)

type Service struct {
	repo      *state.Repository
	providers *provider.Registry
}

func New(repo *state.Repository, providers *provider.Registry) *Service {
	return &Service{repo: repo, providers: providers}
}

func (s *Service) Refresh(ctx context.Context, cfg config.Config) (state.ModelRegistry, error) {
	registry := state.ModelRegistry{
		UpdatedAt: time.Now().UTC(),
		Entries:   map[string][]state.DiscoveredModel{},
	}
	for providerID, providerCfg := range cfg.Providers {
		if !providerCfg.Enabled {
			continue
		}
		if providerCfg.UsesProviderAuth() {
			auth, err := s.repo.LoadAuth(s.repo.ResolveAuthPath(providerCfg.AuthFile))
			if err != nil {
				continue
			}
			refreshed, changed, err := s.providers.RefreshAuth(ctx, auth)
			if err == nil {
				auth = refreshed
				if changed {
					_, _ = s.repo.SaveAuth(auth)
				}
			}
			models, err := s.providers.DiscoverModels(ctx, auth)
			if err != nil {
				continue
			}
			key := providerID
			for _, item := range models {
				registry.Entries[key] = append(registry.Entries[key], state.DiscoveredModel{
					Provider:  providerID,
					AccountID: auth.ID,
					Name:      item.Name,
					Label:     item.Label,
					UpdatedAt: time.Now().UTC(),
				})
			}
			continue
		}
		for _, account := range providerCfg.Accounts {
			auth, err := s.repo.LoadAuth(s.repo.ResolveAuthPath(account.AuthFile))
			if err != nil {
				continue
			}
			refreshed, changed, err := s.providers.RefreshAuth(ctx, auth)
			if err == nil {
				auth = refreshed
				if changed {
					_, _ = s.repo.SaveAuth(auth)
				}
			}
			models, err := s.providers.DiscoverModels(ctx, auth)
			if err != nil {
				continue
			}
			key := providerID + ":" + account.ID
			for _, item := range models {
				registry.Entries[key] = append(registry.Entries[key], state.DiscoveredModel{
					Provider:  providerID,
					AccountID: account.ID,
					Name:      item.Name,
					Label:     item.Label,
					UpdatedAt: time.Now().UTC(),
				})
			}
		}
	}
	if err := s.repo.SaveRegistry(registry); err != nil {
		return state.ModelRegistry{}, err
	}
	return registry, nil
}
