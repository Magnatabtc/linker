package codingplan

import (
	"context"
	"errors"
	"strings"

	"linker/internal/providerkit"
	"linker/internal/state"
)

const endpoint = "https://coding-intl.dashscope.aliyuncs.com/apps/anthropic"

func authenticate(_ context.Context, ui providerkit.Interactive, existing *state.AccountAuth) (state.AccountAuth, error) {
	if existing != nil && strings.TrimSpace(existing.APIKey) != "" {
		if strings.EqualFold(strings.TrimSpace(ui.Prompt("CodingPlan API key already configured. Keep current key? [Y/n]", "Y")), "n") {
		} else {
			return normalizeAuth(*existing), nil
		}
	}

	ui.Println()
	ui.Println("CodingPlan (Alibaba) Setup")
	ui.Println("  This provider uses an API key (not OAuth).")
	apiKey := strings.TrimSpace(ui.Prompt("Enter your CodingPlan API key (sk-sp-...)", ""))
	if apiKey == "" {
		return state.AccountAuth{}, errors.New("CodingPlan requires an API key")
	}
	return normalizeAuth(state.AccountAuth{
		ID:           "codingplan",
		Provider:     "codingplan",
		Email:        "codingplan",
		AuthType:     "api_key",
		APIKey:       apiKey,
		BaseURL:      endpoint,
		UpstreamType: "anthropic",
	}), nil
}

func normalizeAuth(auth state.AccountAuth) state.AccountAuth {
	auth.ID = "codingplan"
	auth.Provider = "codingplan"
	auth.Email = "codingplan"
	auth.AuthType = "api_key"
	auth.BaseURL = endpoint
	auth.UpstreamType = "anthropic"
	return auth
}
