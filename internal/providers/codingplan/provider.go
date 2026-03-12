package codingplan

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

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
		ID:          "codingplan",
		DisplayName: "CodingPlan",
		AuthKind:    "apikey",
		Models:      []string{"qwen3-coder", "qwen3-coder-plus"},
	}
}

func (d *Driver) Authenticate(ctx context.Context, ui providerkit.Interactive, existing *state.AccountAuth) (state.AccountAuth, error) {
	return authenticate(ctx, ui, existing)
}

func (d *Driver) Refresh(_ context.Context, auth state.AccountAuth) (state.AccountAuth, bool, error) {
	return auth, false, nil
}

func (d *Driver) DiscoverModels(_ context.Context, auth state.AccountAuth) ([]providerkit.Model, error) {
	return shared.ModelsFromNames(auth.Provider, auth.Email, d.Info().Models), nil
}

func (d *Driver) Invoke(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest) (compat.NormalizedResponse, state.AccountAuth, error) {
	payload, err := compat.BuildAnthropicUpstreamRequest(req)
	if err != nil {
		return compat.NormalizedResponse{}, auth, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return compat.NormalizedResponse{}, auth, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, auth.BaseURL, bytes.NewReader(body))
	if err != nil {
		return compat.NormalizedResponse{}, auth, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+auth.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	resp, err := shared.HTTPClient(2 * time.Minute).Do(httpReq)
	if err != nil {
		return compat.NormalizedResponse{}, auth, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<22))
	if resp.StatusCode >= 300 {
		return compat.NormalizedResponse{}, auth, &shared.HTTPError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	parsed, err := compat.ParseAnthropicUpstreamResponse(bytes.NewReader(data))
	return parsed, auth, err
}
