package codex

import (
	"context"
	"strings"

	"linker/internal/compat"
	"linker/internal/modelnorm"
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
		Models: []string{
			"gpt-5.4",
			"gpt-5.4-mini",
			"gpt-5.3-codex",
		},
	}
}

func canonicalCodexModelName(name string) string {
	return modelnorm.Normalize("codex", strings.TrimSpace(name))
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
	return invoke(ctx, auth, req)
}
