package providerkit

import (
	"context"

	"linker/internal/compat"
	"linker/internal/platform"
	"linker/internal/state"
)

type Info struct {
	ID          string
	DisplayName string
	AuthKind    string
	Models      []string
}

type Model struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Label    string `json:"label"`
}

type Interactive struct {
	Env     platform.Environment
	Prompt  func(prompt string, fallback string) string
	Printf  func(format string, args ...any)
	Println func(args ...any)
}

type Driver interface {
	Info() Info
	Authenticate(ctx context.Context, ui Interactive, existing *state.AccountAuth) (state.AccountAuth, error)
	Refresh(ctx context.Context, auth state.AccountAuth) (state.AccountAuth, bool, error)
	DiscoverModels(ctx context.Context, auth state.AccountAuth) ([]Model, error)
	Invoke(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest) (compat.NormalizedResponse, state.AccountAuth, error)
}
