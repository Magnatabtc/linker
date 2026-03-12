package shared

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"linker/internal/compat"
	"linker/internal/providerkit"
	"linker/internal/state"
)

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("upstream error (%d): %s", e.StatusCode, strings.TrimSpace(e.Body))
}

func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func DiscoverOpenAIModels(ctx context.Context, auth state.AccountAuth, fallback []string) ([]providerkit.Model, error) {
	body, err := getJSON(ctx, strings.TrimRight(auth.BaseURL, "/")+"/v1/models", openAIHeaders(auth))
	if err != nil {
		return ModelsFromNames(auth.Provider, auth.Email, fallback), err
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Data) == 0 {
		return ModelsFromNames(auth.Provider, auth.Email, fallback), err
	}
	models := make([]providerkit.Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		models = append(models, providerkit.Model{
			Provider: auth.Provider,
			Name:     item.ID,
			Label:    fmt.Sprintf("%s [%s]", item.ID, auth.Email),
		})
	}
	return models, nil
}

func InvokeOpenAI(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest) (compat.NormalizedResponse, error) {
	payload, err := compat.BuildOpenAIUpstreamRequest(req)
	if err != nil {
		return compat.NormalizedResponse{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return compat.NormalizedResponse{}, err
	}
	responseBody, err := postJSON(ctx, strings.TrimRight(auth.BaseURL, "/")+"/v1/chat/completions", body, openAIHeaders(auth))
	if err != nil {
		return compat.NormalizedResponse{}, err
	}
	return compat.ParseOpenAIUpstreamResponse(bytes.NewReader(responseBody))
}

func DiscoverAnthropicModels(ctx context.Context, auth state.AccountAuth, fallback []string) ([]providerkit.Model, error) {
	body, err := getJSON(ctx, strings.TrimRight(auth.BaseURL, "/")+"/v1/models", anthropicHeaders(auth))
	if err != nil {
		return ModelsFromNames(auth.Provider, auth.Email, fallback), err
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Data) == 0 {
		return ModelsFromNames(auth.Provider, auth.Email, fallback), err
	}
	models := make([]providerkit.Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		models = append(models, providerkit.Model{
			Provider: auth.Provider,
			Name:     item.ID,
			Label:    fmt.Sprintf("%s [%s]", item.ID, auth.Email),
		})
	}
	return models, nil
}

func InvokeAnthropic(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest) (compat.NormalizedResponse, error) {
	payload, err := compat.BuildAnthropicUpstreamRequest(req)
	if err != nil {
		return compat.NormalizedResponse{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return compat.NormalizedResponse{}, err
	}
	responseBody, err := postJSON(ctx, strings.TrimRight(auth.BaseURL, "/")+"/v1/messages", body, anthropicHeaders(auth))
	if err != nil {
		return compat.NormalizedResponse{}, err
	}
	return compat.ParseAnthropicUpstreamResponse(bytes.NewReader(responseBody))
}

func getJSON(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := HTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return body, nil
}

func postJSON(ctx context.Context, url string, payload []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := HTTPClient(2 * time.Minute).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return body, nil
}

func openAIHeaders(auth state.AccountAuth) map[string]string {
	headers := map[string]string{}
	if auth.APIKey != "" {
		headers["Authorization"] = "Bearer " + auth.APIKey
	}
	if auth.AccessToken != "" {
		headers["Authorization"] = "Bearer " + auth.AccessToken
	}
	for key, value := range auth.Metadata {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "header:") {
			headers[key[len("header:"):]] = value
		}
	}
	return headers
}

func anthropicHeaders(auth state.AccountAuth) map[string]string {
	headers := map[string]string{
		"anthropic-version": "2023-06-01",
	}
	if auth.APIKey != "" {
		headers["x-api-key"] = auth.APIKey
		headers["Authorization"] = "Bearer " + auth.APIKey
	}
	if auth.AccessToken != "" {
		headers["x-api-key"] = auth.AccessToken
		headers["Authorization"] = "Bearer " + auth.AccessToken
	}
	for key, value := range auth.Metadata {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "header:") {
			headers[key[len("header:"):]] = value
		}
	}
	return headers
}

func ModelsFromNames(providerID string, labelSource string, names []string) []providerkit.Model {
	models := make([]providerkit.Model, 0, len(names))
	for _, name := range names {
		models = append(models, providerkit.Model{
			Provider: providerID,
			Name:     name,
			Label:    fmt.Sprintf("%s [%s]", name, labelSource),
		})
	}
	return models
}
