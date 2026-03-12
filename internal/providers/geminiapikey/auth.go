package geminiapikey

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"linker/internal/providerkit"
	"linker/internal/providers/shared"
	"linker/internal/state"
)

const (
	providerID  = "gemini-apikey"
	validateURL = "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1000"
	baseURL     = "https://generativelanguage.googleapis.com/v1beta/openai"
)

func authenticate(ctx context.Context, ui providerkit.Interactive, existing *state.AccountAuth) (state.AccountAuth, error) {
	auth := state.AccountAuth{
		ID:               providerID,
		Provider:         providerID,
		Email:            "gemini-apikey-pool",
		AuthType:         "apikey_pool",
		BaseURL:          baseURL,
		UpstreamType:     "openai",
		RotationStrategy: "round-robin",
	}
	if existing != nil {
		auth = normalizePool(*existing)
	}

	ui.Println()
	ui.Println("Gemini API Key Setup")
	ui.Println("  Add your Google AI Studio API keys.")
	ui.Println("  Get free keys at: https://aistudio.google.com/apikey")

	if len(auth.Keys) > 0 {
		ui.Printf("  %d key(s) currently in pool\n", len(auth.Keys))
		for idx, key := range auth.Keys {
			status := key.Status
			if status == "" {
				status = "active"
			}
			ui.Printf("    [%d] %s %s\n", idx+1, key.Label, MaskKey(key.Key)+" "+status)
		}
		choice := strings.ToUpper(strings.TrimSpace(ui.Prompt("Options: [A]dd [R]emove [T]est [K]eep", "K")))
		switch choice {
		case "K":
			return auth, nil
		case "R":
			auth.Keys = removeKeysInteractive(ui, auth.Keys)
		case "T":
			var err error
			auth, err = TestPool(ctx, auth)
			if err != nil {
				ui.Printf("  Test warning: %v\n", err)
			}
			return auth, nil
		}
	}

	for {
		keyValue := strings.TrimSpace(ui.Prompt(fmt.Sprintf("Enter API key #%d", len(auth.Keys)+1), ""))
		if keyValue == "" {
			if len(auth.Keys) > 0 {
				break
			}
			ui.Println("  At least one API key is required.")
			continue
		}
		if err := validateKey(ctx, keyValue); err != nil {
			ui.Printf("  Invalid key: %v\n", err)
			continue
		}
		entry := state.APIKeyEntry{
			Key:     keyValue,
			Label:   fmt.Sprintf("key-%d", len(auth.Keys)+1),
			AddedAt: time.Now().UTC(),
			Status:  "active",
		}
		auth.Keys = append(auth.Keys, entry)
		ui.Printf("  %s added (verified)\n", entry.Label)
		if strings.ToLower(strings.TrimSpace(ui.Prompt("Add another key? [y/N]", "N"))) != "y" {
			break
		}
	}

	if len(auth.Keys) == 0 {
		return state.AccountAuth{}, fmt.Errorf("Gemini API Key requires at least one valid key")
	}
	return normalizePool(auth), nil
}

func normalizePool(auth state.AccountAuth) state.AccountAuth {
	auth.ID = providerID
	auth.Provider = providerID
	auth.Email = "gemini-apikey-pool"
	auth.AuthType = "apikey_pool"
	auth.BaseURL = baseURL
	auth.UpstreamType = "openai"
	if auth.RotationStrategy == "" {
		auth.RotationStrategy = "round-robin"
	}
	for i := range auth.Keys {
		if auth.Keys[i].Label == "" {
			auth.Keys[i].Label = fmt.Sprintf("key-%d", i+1)
		}
		if auth.Keys[i].AddedAt.IsZero() {
			auth.Keys[i].AddedAt = time.Now().UTC()
		}
		if auth.Keys[i].Status == "" {
			auth.Keys[i].Status = "active"
		}
	}
	return auth
}

func validateKey(ctx context.Context, keyValue string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, validateURL+"&key="+keyValue, nil)
	if err != nil {
		return err
	}
	resp, err := shared.HTTPClient(20 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("validation returned status %d", resp.StatusCode)
	}
	return nil
}

func TestPool(ctx context.Context, auth state.AccountAuth) (state.AccountAuth, error) {
	auth = normalizePool(auth)
	var firstErr error
	for i := range auth.Keys {
		err := validateKey(ctx, auth.Keys[i].Key)
		if err != nil {
			auth.Keys[i].Status = "invalid"
			auth.Keys[i].LastError = err.Error()
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		auth.Keys[i].Status = "active"
		auth.Keys[i].LastError = ""
	}
	return auth, firstErr
}

func MaskKey(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		return value
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func removeKeysInteractive(ui providerkit.Interactive, keys []state.APIKeyEntry) []state.APIKeyEntry {
	if len(keys) == 0 {
		return keys
	}
	raw := strings.TrimSpace(ui.Prompt("Enter the key number to remove", ""))
	var index int
	if _, err := fmt.Sscanf(raw, "%d", &index); err != nil || index <= 0 || index > len(keys) {
		ui.Println("  No key removed.")
		return keys
	}
	result := make([]state.APIKeyEntry, 0, len(keys)-1)
	for i, key := range keys {
		if i == index-1 {
			continue
		}
		result = append(result, key)
	}
	return result
}
