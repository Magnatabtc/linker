package geminiapikey

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"linker/internal/compat"
	"linker/internal/providerkit"
	"linker/internal/providers/shared"
	"linker/internal/state"
)

const rateLimitCooldown = 60 * time.Second

type Driver struct{}

func New() *Driver {
	return &Driver{}
}

func (d *Driver) Info() providerkit.Info {
	return providerkit.Info{
		ID:          providerID,
		DisplayName: "Gemini API Key",
		AuthKind:    "apikey-pool",
		Models:      []string{"gemini-2.5-pro", "gemini-2.5-flash"},
	}
}

func (d *Driver) Authenticate(ctx context.Context, ui providerkit.Interactive, existing *state.AccountAuth) (state.AccountAuth, error) {
	return authenticate(ctx, ui, existing)
}

func (d *Driver) Refresh(_ context.Context, auth state.AccountAuth) (state.AccountAuth, bool, error) {
	return normalizePool(auth), false, nil
}

func (d *Driver) DiscoverModels(_ context.Context, auth state.AccountAuth) ([]providerkit.Model, error) {
	auth = normalizePool(auth)
	models := make([]providerkit.Model, 0, len(d.Info().Models))
	for _, name := range d.Info().Models {
		models = append(models, providerkit.Model{
			Provider: providerID,
			Name:     name,
			Label:    fmt.Sprintf("%s [Gemini API Key pool: %d keys]", name, len(auth.Keys)),
		})
	}
	return models, nil
}

func (d *Driver) Invoke(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest) (compat.NormalizedResponse, state.AccountAuth, error) {
	auth = normalizePool(auth)
	if len(auth.Keys) == 0 {
		return compat.NormalizedResponse{}, auth, fmt.Errorf("Gemini API Key pool is empty")
	}

	var lastErr error
	for attempts := 0; attempts < len(auth.Keys); attempts++ {
		index, entry, ok := nextEligibleKey(&auth)
		if !ok {
			break
		}
		callAuth := auth
		callAuth.APIKey = entry.Key
		callAuth.BaseURL = baseURL
		callAuth.UpstreamType = "openai"

		now := time.Now().UTC()
		auth.Keys[index].Requests++
		auth.Keys[index].LastUsedAt = &now

		resp, err := shared.InvokeOpenAI(ctx, callAuth, req)
		if err == nil {
			auth.Keys[index].Status = "active"
			auth.Keys[index].LastError = ""
			return resp, auth, nil
		}

		lastErr = err
		statusErr, ok := err.(*shared.HTTPError)
		if ok && (statusErr.StatusCode == 429 || statusErr.StatusCode == 403) {
			auth.Keys[index].RateLimits++
			auth.Keys[index].LastRateLimited = &now
			auth.Keys[index].LastError = strings.TrimSpace(statusErr.Body)
			continue
		}
		auth.Keys[index].LastError = err.Error()
		return compat.NormalizedResponse{}, auth, err
	}

	if lastErr == nil {
		payload, _ := json.Marshal(map[string]any{
			"error": map[string]any{
				"type":    "rate_limit_error",
				"message": "All Gemini API keys in pool are rate-limited. Add more keys with 'linker apikey add gemini' or wait.",
			},
		})
		lastErr = &shared.HTTPError{StatusCode: 429, Body: string(payload)}
	}
	return compat.NormalizedResponse{}, auth, lastErr
}

func nextEligibleKey(auth *state.AccountAuth) (int, state.APIKeyEntry, bool) {
	auth.RotationIndex = auth.RotationIndex % len(auth.Keys)
	now := time.Now().UTC()
	for offset := 0; offset < len(auth.Keys); offset++ {
		index := (auth.RotationIndex + offset) % len(auth.Keys)
		key := auth.Keys[index]
		if strings.EqualFold(key.Status, "invalid") {
			continue
		}
		if key.LastRateLimited != nil && now.Sub(*key.LastRateLimited) < rateLimitCooldown {
			continue
		}
		auth.RotationIndex = (index + 1) % len(auth.Keys)
		return index, key, true
	}
	return 0, state.APIKeyEntry{}, false
}
