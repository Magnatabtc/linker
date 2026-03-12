package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
		ID:          "gemini-cli",
		DisplayName: "Gemini CLI",
		AuthKind:    "oauth",
		Models:      []string{"gemini-2.5-pro", "gemini-2.5-flash"},
	}
}

func (d *Driver) Authenticate(ctx context.Context, ui providerkit.Interactive, existing *state.AccountAuth) (state.AccountAuth, error) {
	return authenticate(ctx, ui, existing)
}

func (d *Driver) Refresh(ctx context.Context, auth state.AccountAuth) (state.AccountAuth, bool, error) {
	return refresh(ctx, auth)
}

func (d *Driver) DiscoverModels(ctx context.Context, auth state.AccountAuth) ([]providerkit.Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return shared.DiscoverOpenAIModels(ctx, auth, d.Info().Models)
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	resp, err := shared.HTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return shared.DiscoverOpenAIModels(ctx, auth, d.Info().Models)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return shared.DiscoverOpenAIModels(ctx, auth, d.Info().Models)
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Models) == 0 {
		return shared.DiscoverOpenAIModels(ctx, auth, d.Info().Models)
	}
	models := make([]providerkit.Model, 0, len(payload.Models))
	for _, item := range payload.Models {
		name := strings.TrimPrefix(item.Name, "models/")
		if name == "" || strings.Contains(strings.ToLower(name), "embedding") {
			continue
		}
		models = append(models, providerkit.Model{
			Provider: auth.Provider,
			Name:     name,
			Label:    fmt.Sprintf("%s [%s]", name, auth.Email),
		})
	}
	if len(models) == 0 {
		return shared.DiscoverOpenAIModels(ctx, auth, d.Info().Models)
	}
	return models, nil
}

func (d *Driver) Invoke(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest) (compat.NormalizedResponse, state.AccountAuth, error) {
	auth.BaseURL = openAIBaseURL
	resp, err := shared.InvokeOpenAI(ctx, auth, req)
	return resp, auth, err
}
