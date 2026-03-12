package provider

import (
	"context"
	"fmt"

	"linker/internal/compat"
	"linker/internal/providerkit"
	"linker/internal/providers/antigravity"
	"linker/internal/providers/codex"
	"linker/internal/providers/codingplan"
	"linker/internal/providers/gemini"
	"linker/internal/providers/geminiapikey"
	"linker/internal/state"
)

type Info = providerkit.Info
type Model = providerkit.Model

type Registry struct {
	drivers map[string]providerkit.Driver
	order   []string
}

func NewRegistry() *Registry {
	drivers := map[string]providerkit.Driver{
		"gemini-cli":    gemini.New(),
		"antigravity":   antigravity.New(),
		"gemini-apikey": geminiapikey.New(),
		"codex":         codex.New(),
		"codingplan":    codingplan.New(),
	}
	return &Registry{
		drivers: drivers,
		order:   []string{"gemini-cli", "antigravity", "gemini-apikey", "codex", "codingplan"},
	}
}

func (r *Registry) List() []Info {
	result := make([]Info, 0, len(r.drivers))
	for _, id := range r.order {
		driver, ok := r.drivers[id]
		if !ok {
			continue
		}
		result = append(result, driver.Info())
	}
	return result
}

func (r *Registry) Get(id string) (Info, bool) {
	driver, ok := r.drivers[id]
	if !ok {
		return Info{}, false
	}
	return driver.Info(), true
}

func (r *Registry) Driver(id string) (providerkit.Driver, bool) {
	driver, ok := r.drivers[id]
	return driver, ok
}

func (r *Registry) Authenticate(ctx context.Context, providerID string, ui providerkit.Interactive, existing *state.AccountAuth) (state.AccountAuth, error) {
	driver, ok := r.drivers[providerID]
	if !ok {
		return state.AccountAuth{}, fmt.Errorf("unknown provider %q", providerID)
	}
	return driver.Authenticate(ctx, ui, existing)
}

func (r *Registry) BuiltinModels(providerID string) []Model {
	driver, ok := r.drivers[providerID]
	if !ok {
		return nil
	}
	info := driver.Info()
	models := make([]Model, 0, len(info.Models))
	for _, name := range info.Models {
		models = append(models, Model{
			Provider: providerID,
			Name:     name,
			Label:    fmt.Sprintf("%s [%s]", name, info.DisplayName),
		})
	}
	return models
}

func (r *Registry) DiscoverModels(ctx context.Context, auth state.AccountAuth) ([]Model, error) {
	driver, ok := r.drivers[auth.Provider]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", auth.Provider)
	}
	models, err := driver.DiscoverModels(ctx, auth)
	if err != nil && len(models) == 0 {
		return r.BuiltinModels(auth.Provider), err
	}
	if len(models) == 0 {
		return r.BuiltinModels(auth.Provider), nil
	}
	return models, err
}

func (r *Registry) RefreshAuth(ctx context.Context, auth state.AccountAuth) (state.AccountAuth, bool, error) {
	driver, ok := r.drivers[auth.Provider]
	if !ok {
		return auth, false, fmt.Errorf("unknown provider %q", auth.Provider)
	}
	return driver.Refresh(ctx, auth)
}

func (r *Registry) Invoke(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest) (compat.NormalizedResponse, state.AccountAuth, error) {
	driver, ok := r.drivers[auth.Provider]
	if !ok {
		return compat.NormalizedResponse{}, auth, fmt.Errorf("unknown provider %q", auth.Provider)
	}
	refreshed, _, err := driver.Refresh(ctx, auth)
	if err != nil {
		return compat.NormalizedResponse{}, auth, err
	}
	return driver.Invoke(ctx, refreshed, req)
}
