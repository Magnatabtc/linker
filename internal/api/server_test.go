package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"linker/internal/catalog"
	"linker/internal/config"
	"linker/internal/provider"
	"linker/internal/state"
)

func TestHandleAnthropicRequiresLocalAuth(t *testing.T) {
	server := newTestServer(t, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"ping"}],"max_tokens":8}`))
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	server.handleAnthropic(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "missing local auth token") {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}
}

func TestHandleAnthropicRoutesMappedModelToProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected upstream authorization: %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("unexpected anthropic-version: %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream request body: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if payload["model"] != "qwen3-coder" {
			t.Fatalf("expected mapped model qwen3-coder, got %#v", payload["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_test","model":"qwen3-coder","stop_reason":"end_turn","content":[{"type":"text","text":"ok from upstream"}],"usage":{"input_tokens":2,"output_tokens":3}}`))
	}))
	defer upstream.Close()

	server := newTestServer(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"ping"}],"max_tokens":16}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer linker-local")
	rr := httptest.NewRecorder()

	server.handleAnthropic(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode anthropic response: %v", err)
	}
	content, ok := response["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected content array in response: %#v", response)
	}
	part, ok := content[0].(map[string]any)
	if !ok || part["text"] != "ok from upstream" {
		t.Fatalf("unexpected first content part: %#v", content[0])
	}
}

func TestHandleOpenAIRoutesMappedModelToProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_test","model":"qwen3-coder","stop_reason":"end_turn","content":[{"type":"text","text":"ok from upstream"}],"usage":{"input_tokens":2,"output_tokens":3}}`))
	}))
	defer upstream.Close()

	server := newTestServer(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"ping"}],"max_completion_tokens":16}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer linker-local")
	rr := httptest.NewRecorder()

	server.handleOpenAI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode openai response: %v", err)
	}
	choices, ok := response["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("expected choices in response: %#v", response)
	}
}

func newTestServer(t *testing.T, upstreamURL string) *Server {
	t.Helper()

	root := t.TempDir()
	layout := state.NewLayout(root)
	repo := state.NewRepository(layout)
	if err := repo.Init(); err != nil {
		t.Fatalf("init repository: %v", err)
	}

	if upstreamURL == "" {
		upstreamURL = "https://example.invalid"
	}
	authPath, err := repo.SaveAuth(state.AccountAuth{
		ID:           "codingplan",
		Provider:     "codingplan",
		Email:        "codingplan",
		AuthType:     "api_key",
		APIKey:       "test-key",
		BaseURL:      upstreamURL,
		UpstreamType: "anthropic",
	})
	if err != nil {
		t.Fatalf("save auth: %v", err)
	}

	cfg := config.Default(filepath.Join(root, ".claude", "settings.json"))
	cfg.Providers["codingplan"] = config.ProviderConfig{
		Type:     "apikey",
		Enabled:  true,
		AuthFile: repo.Relative(authPath),
	}
	cfg.ModelMapping.Default = config.ModelTarget{Provider: "codingplan", Model: "qwen3-coder"}
	cfg.ModelMapping.Opus = config.ModelTarget{Provider: "codingplan", Model: "qwen3-coder"}
	cfg.ModelMapping.Sonnet = config.ModelTarget{Provider: "codingplan", Model: "qwen3-coder"}
	cfg.ModelMapping.Haiku = config.ModelTarget{Provider: "codingplan", Model: "qwen3-coder"}

	store := config.NewStore(layout.ConfigFile)
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	providers := provider.NewRegistry()
	catalogService := catalog.New(repo, providers)
	return New(cfg, repo, store, providers, catalogService, nil)
}

func TestHasLocalAuth(t *testing.T) {
	withToken := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	withToken.RemoteAddr = "127.0.0.1:12345"
	withToken.Header.Set("Authorization", "Bearer linker-local")
	if !hasLocalAuth(withToken) {
		t.Fatal("expected bearer token to be accepted")
	}

	withoutToken := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	withoutToken.RemoteAddr = "127.0.0.1:12345"
	if hasLocalAuth(withoutToken) {
		t.Fatal("expected missing token to be rejected")
	}

	emptyBearer := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	emptyBearer.RemoteAddr = "127.0.0.1:12345"
	emptyBearer.Header.Set("Authorization", "Bearer ")
	if hasLocalAuth(emptyBearer) {
		t.Fatal("expected empty bearer token to be rejected")
	}
}
