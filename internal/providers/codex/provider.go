package codex

import (
	"context"

	"linker/internal/compat"
	"linker/internal/providerkit"
	"linker/internal/providers/shared"
	"linker/internal/state"
)

type Driver struct{}

func New() *Driver {
	return &Driver{}
}

func (d *Driver) Info() providerkit.Info {
	return providerkit.Info{
		ID:          "codex",
		DisplayName: "Codex",
		AuthKind:    "oauth",
		Models:      []string{"gpt-5-codex", "gpt-5-mini-codex"},
	}
}

func (d *Driver) Authenticate(ctx context.Context, ui providerkit.Interactive, existing *state.AccountAuth) (state.AccountAuth, error) {
	return authenticate(ctx, ui, existing)
}

func (d *Driver) Refresh(ctx context.Context, auth state.AccountAuth) (state.AccountAuth, bool, error) {
	return refresh(ctx, auth)
}

func (d *Driver) DiscoverModels(ctx context.Context, auth state.AccountAuth) ([]providerkit.Model, error) {
	return shared.DiscoverOpenAIModels(ctx, auth, d.Info().Models)
}

func (d *Driver) Invoke(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest) (compat.NormalizedResponse, state.AccountAuth, error) {
	resp, err := shared.InvokeOpenAI(ctx, auth, req)
	return resp, auth, err
}
