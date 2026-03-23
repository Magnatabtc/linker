package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"linker/internal/compat"
	"linker/internal/state"
)

func TestNormalizeAuthDefaultsCodexBackend(t *testing.T) {
	t.Parallel()

	auth := normalizeAuth(state.AccountAuth{})
	if auth.Provider != "codex" {
		t.Fatalf("provider = %q, want %q", auth.Provider, "codex")
	}
	if auth.AuthType != "oauth" {
		t.Fatalf("auth type = %q, want %q", auth.AuthType, "oauth")
	}
	if auth.BaseURL != codexBackendBaseURL {
		t.Fatalf("base url = %q, want %q", auth.BaseURL, codexBackendBaseURL)
	}
	if auth.UpstreamType != "codex" {
		t.Fatalf("upstream type = %q, want %q", auth.UpstreamType, "codex")
	}
	if auth.TokenURL != tokenURL || auth.RefreshURL != tokenURL || auth.AuthorizationURL != authURL {
		t.Fatalf("unexpected codex auth metadata: %#v", auth)
	}
}

func TestInvokeUsesCodexResponsesTransport(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotHeaders http.Header
	var gotBody []byte
	var handlerErr error

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		gotBody, handlerErr = io.ReadAll(r.Body)
		if handlerErr != nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"type":"response.completed",
			"response":{
				"id":"resp_1",
				"model":"gpt-5.4",
				"status":"completed",
				"output":[
					{"type":"message","content":[{"type":"output_text","text":"ok from codex"}]},
					{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"city\":\"Paris\"}"}
				],
				"usage":{"input_tokens":12,"output_tokens":4,"total_tokens":16}
			}
		}`))
	}))
	defer server.Close()

	auth := state.AccountAuth{
		AccessToken: "test-token",
		Metadata: map[string]string{
			"base_url":   server.URL,
			"account_id": "acct_123",
		},
	}
	req := compat.NormalizedRequest{
		Model:  "gpt-5-codex",
		System: "You are helpful",
		Messages: []compat.Message{
			{
				Role: "user",
				Parts: []compat.ContentPart{
					{Type: "text", Text: "hello"},
				},
			},
			{
				Role: "assistant",
				Parts: []compat.ContentPart{
					{Type: "text", Text: "thinking"},
					{Type: "tool_use", ToolID: "call_1", Name: "lookup", Input: map[string]any{"city": "Paris"}},
				},
			},
			{
				Role: "tool",
				Parts: []compat.ContentPart{
					{Type: "tool_result", ToolID: "call_1", Text: `{"ok":true}`},
				},
			},
		},
		Tools: []compat.Tool{
			{
				Name:        "lookup",
				Description: "Find a place",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
		},
	}

	resp, updatedAuth, err := New().Invoke(context.Background(), auth, req)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if handlerErr != nil {
		t.Fatalf("read request body: %v", handlerErr)
	}

	if gotPath != "/responses/compact" {
		t.Fatalf("path = %q, want %q", gotPath, "/responses/compact")
	}
	if gotHeaders.Get("Authorization") != "Bearer test-token" {
		t.Fatalf("authorization = %q", gotHeaders.Get("Authorization"))
	}
	if gotHeaders.Get("Accept") != "application/json" {
		t.Fatalf("accept = %q", gotHeaders.Get("Accept"))
	}
	if gotHeaders.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %q", gotHeaders.Get("Content-Type"))
	}
	if gotHeaders.Get("Version") != codexVersionHeader {
		t.Fatalf("version = %q, want %q", gotHeaders.Get("Version"), codexVersionHeader)
	}
	if gotHeaders.Get("User-Agent") != codexUserAgent {
		t.Fatalf("user-agent = %q, want %q", gotHeaders.Get("User-Agent"), codexUserAgent)
	}
	if gotHeaders.Get("Originator") != "codex_cli_rs" {
		t.Fatalf("originator = %q", gotHeaders.Get("Originator"))
	}
	if gotHeaders.Get("Chatgpt-Account-Id") != "acct_123" {
		t.Fatalf("account id = %q", gotHeaders.Get("Chatgpt-Account-Id"))
	}
	if gotHeaders.Get("Session_id") == "" {
		t.Fatal("expected session id header to be set")
	}

	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if _, ok := payload["messages"]; ok {
		t.Fatalf("unexpected chat-completions messages field: %#v", payload["messages"])
	}
	if got := payload["model"]; got != "gpt-5.4" {
		t.Fatalf("model = %#v, want %q", got, "gpt-5.4")
	}
	if got := payload["instructions"]; got != req.System {
		t.Fatalf("instructions = %#v, want %q", got, req.System)
	}
	if _, ok := payload["store"]; ok {
		t.Fatalf("unexpected store field: %#v", payload["store"])
	}
	if _, ok := payload["parallel_tool_calls"]; ok {
		t.Fatalf("unexpected parallel_tool_calls field: %#v", payload["parallel_tool_calls"])
	}
	if _, ok := payload["include"]; ok {
		t.Fatalf("unexpected include field: %#v", payload["include"])
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", payload["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool = %#v", tools[0])
	}
	if tool["type"] != "function" || tool["name"] != "lookup" {
		t.Fatalf("unexpected tool payload: %#v", tool)
	}
	if tool["description"] != "Find a place" {
		t.Fatalf("tool description = %#v", tool["description"])
	}
	if _, ok := tool["parameters"]; !ok {
		t.Fatal("expected tool parameters to be present")
	}

	input, ok := payload["input"].([]any)
	if !ok {
		t.Fatalf("input = %#v", payload["input"])
	}
	if len(input) != 4 {
		t.Fatalf("input length = %d, want 4", len(input))
	}
	first, _ := input[0].(map[string]any)
	if first["type"] != "message" || first["role"] != "user" {
		t.Fatalf("unexpected first input item: %#v", first)
	}
	content, _ := first["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("first content = %#v", first["content"])
	}
	part, _ := content[0].(map[string]any)
	if part["type"] != "input_text" || part["text"] != "hello" {
		t.Fatalf("unexpected first content part: %#v", part)
	}

	if resp.ID != "resp_1" || resp.Model != "gpt-5.4" {
		t.Fatalf("unexpected response metadata: %#v", resp)
	}
	if resp.StopReason != "tool_calls" {
		t.Fatalf("stop reason = %q, want %q", resp.StopReason, "tool_calls")
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 4 {
		t.Fatalf("unexpected usage: %#v", resp.Usage)
	}
	if len(resp.Parts) != 2 {
		t.Fatalf("parts = %#v", resp.Parts)
	}
	if resp.Parts[0].Type != "text" || resp.Parts[0].Text != "ok from codex" {
		t.Fatalf("unexpected first response part: %#v", resp.Parts[0])
	}
	if resp.Parts[1].Type != "tool_use" || resp.Parts[1].ToolID != "call_1" || resp.Parts[1].Name != "lookup" {
		t.Fatalf("unexpected tool response part: %#v", resp.Parts[1])
	}
	if resp.Parts[1].Input["city"] != "Paris" {
		t.Fatalf("unexpected tool input: %#v", resp.Parts[1].Input)
	}

	if updatedAuth.Provider != "codex" || updatedAuth.UpstreamType != "codex" || updatedAuth.BaseURL != codexBackendBaseURL {
		t.Fatalf("unexpected normalized auth: %#v", updatedAuth)
	}
}

func TestInvokeRetriesMiniModelOnceWhenUnsupported(t *testing.T) {
	t.Parallel()

	var requestCount int
	var firstModel string
	var secondModel string
	var gotHeaders http.Header
	var handlerErr error
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			mu.Lock()
			handlerErr = err
			mu.Unlock()
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			mu.Lock()
			handlerErr = err
			mu.Unlock()
			return
		}
		switch requestCount {
		case 1:
			firstModel, _ = payload["model"].(string)
			gotHeaders = r.Header.Clone()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"gpt-5.4-mini not supported"}`))
		case 2:
			secondModel, _ = payload["model"].(string)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"type":"response.completed",
				"response":{
					"id":"resp_retry",
					"model":"gpt-5.4",
					"status":"completed",
					"output":[{"type":"message","content":[{"type":"output_text","text":"fallback ok"}]}],
					"usage":{"input_tokens":7,"output_tokens":2,"total_tokens":9}
				}
			}`))
		default:
			mu.Lock()
			handlerErr = fmt.Errorf("unexpected extra request %d", requestCount)
			mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	auth := state.AccountAuth{
		AccessToken: "test-token",
		Metadata: map[string]string{
			"base_url":   server.URL,
			"account_id": "acct_123",
		},
	}

	resp, _, err := New().Invoke(context.Background(), auth, compat.NormalizedRequest{
		Model: "gpt-5-mini-codex",
		Messages: []compat.Message{
			{
				Role:  "user",
				Parts: []compat.ContentPart{{Type: "text", Text: "hello"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("invoke with fallback: %v", err)
	}
	mu.Lock()
	err = handlerErr
	mu.Unlock()
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if requestCount != 2 {
		t.Fatalf("request count = %d, want 2", requestCount)
	}
	if firstModel != "gpt-5.4-mini" {
		t.Fatalf("first model = %q, want %q", firstModel, "gpt-5.4-mini")
	}
	if secondModel != "gpt-5.4" {
		t.Fatalf("second model = %q, want %q", secondModel, "gpt-5.4")
	}
	if gotHeaders.Get("Authorization") != "Bearer test-token" {
		t.Fatalf("authorization = %q", gotHeaders.Get("Authorization"))
	}
	if resp.ID != "resp_retry" || resp.Model != "gpt-5.4" {
		t.Fatalf("unexpected response metadata: %#v", resp)
	}
	if resp.Text() != "fallback ok" {
		t.Fatalf("response text = %q, want %q", resp.Text(), "fallback ok")
	}
}

func TestInvokeRetriesMiniModelOnNestedErrorDetail(t *testing.T) {
	t.Parallel()

	var requestCount int
	var firstModel string
	var secondModel string
	var handlerErr error
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			mu.Lock()
			handlerErr = err
			mu.Unlock()
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			mu.Lock()
			handlerErr = err
			mu.Unlock()
			return
		}
		switch requestCount {
		case 1:
			firstModel, _ = payload["model"].(string)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"detail":"gpt-5.4-mini not supported"}}`))
		case 2:
			secondModel, _ = payload["model"].(string)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"type":"response.completed",
				"response":{
					"id":"resp_nested_retry",
					"model":"gpt-5.4",
					"status":"completed",
					"output":[{"type":"message","content":[{"type":"output_text","text":"nested fallback ok"}]}],
					"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}
				}
			}`))
		default:
			mu.Lock()
			handlerErr = fmt.Errorf("unexpected extra request %d", requestCount)
			mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	auth := state.AccountAuth{
		AccessToken: "test-token",
		Metadata: map[string]string{
			"base_url":   server.URL,
			"account_id": "acct_123",
		},
	}

	resp, _, err := New().Invoke(context.Background(), auth, compat.NormalizedRequest{
		Model: "gpt-5-mini-codex",
		Messages: []compat.Message{
			{
				Role:  "user",
				Parts: []compat.ContentPart{{Type: "text", Text: "hello"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("invoke with nested fallback: %v", err)
	}
	mu.Lock()
	err = handlerErr
	mu.Unlock()
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if requestCount != 2 {
		t.Fatalf("request count = %d, want 2", requestCount)
	}
	if firstModel != "gpt-5.4-mini" {
		t.Fatalf("first model = %q, want %q", firstModel, "gpt-5.4-mini")
	}
	if secondModel != "gpt-5.4" {
		t.Fatalf("second model = %q, want %q", secondModel, "gpt-5.4")
	}
	if resp.ID != "resp_nested_retry" || resp.Model != "gpt-5.4" {
		t.Fatalf("unexpected response metadata: %#v", resp)
	}
	if resp.Text() != "nested fallback ok" {
		t.Fatalf("response text = %q, want %q", resp.Text(), "nested fallback ok")
	}
}
