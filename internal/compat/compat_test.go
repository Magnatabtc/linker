package compat

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseAnthropicRequest(t *testing.T) {
	t.Parallel()

	body := `{
	  "model": "claude-sonnet-4-20250514",
	  "system": "You are helpful",
	  "messages": [
	    {"role":"user","content":[{"type":"text","text":"hello"}]}
	  ],
	  "tools": [{"name":"lookup","description":"Find things","input_schema":{"type":"object"}}],
	  "stream": true,
	  "max_tokens": 100
	}`

	req, err := ParseAnthropicRequest(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse anthropic request: %v", err)
	}
	if req.Model != "claude-sonnet-4-20250514" || req.System != "You are helpful" || !req.Stream || req.MaxTokens != 100 {
		t.Fatalf("unexpected request: %#v", req)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "lookup" {
		t.Fatalf("unexpected tools: %#v", req.Tools)
	}
}

func TestParseAnthropicToolResult(t *testing.T) {
	t.Parallel()

	body := `{
	  "model": "claude-sonnet-4-20250514",
	  "messages": [
	    {"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"lookup","input":{"city":"Fortaleza"}}]},
	    {"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"{\"ok\":true}"}]}
	  ]
	}`

	req, err := ParseAnthropicRequest(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse anthropic request: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 normalized messages, got %d", len(req.Messages))
	}
	if req.Messages[1].Role != "tool" || req.Messages[1].Parts[0].ToolID != "tool_1" {
		t.Fatalf("unexpected tool result normalization: %#v", req.Messages[1])
	}
}

func TestParseOpenAIRequest(t *testing.T) {
	t.Parallel()

	body := `{
	  "model": "gpt-5.4",
	  "messages": [
	    {"role":"system","content":"You are helpful"},
	    {"role":"user","content":"hello"}
	  ],
	  "stream": false,
	  "max_completion_tokens": 42
	}`

	req, err := ParseOpenAIRequest(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse openai request: %v", err)
	}
	if req.Model != "gpt-5.4" || req.System != "You are helpful" || req.MaxTokens != 42 {
		t.Fatalf("unexpected request: %#v", req)
	}
	if len(req.Messages) != 1 || req.Messages[0].Parts[0].Text != "hello" {
		t.Fatalf("unexpected messages: %#v", req.Messages)
	}
}

func TestWriteAnthropicStream(t *testing.T) {
	t.Parallel()

	resp := NormalizedResponse{
		ID:    "msg_123",
		Model: "gemini-2.5-pro",
		Parts: []ContentPart{
			{Type: "text", Text: "hello world"},
			{Type: "tool_use", ToolID: "tool_1", Name: "lookup", Input: map[string]any{"city": "Fortaleza"}},
		},
		StopReason: "tool_use",
		Usage: Usage{
			InputTokens:  12,
			OutputTokens: 8,
		},
	}

	var buf bytes.Buffer
	if err := WriteAnthropicStream(&buf, resp); err != nil {
		t.Fatalf("write stream: %v", err)
	}
	out := buf.String()
	for _, marker := range []string{
		"event: message_start",
		"event: content_block_start",
		"event: content_block_delta",
		"event: content_block_stop",
		"event: message_delta",
		"event: message_stop",
		"\"type\":\"tool_use\"",
		"\"type\":\"input_json_delta\"",
		"\"stop_reason\":\"tool_use\"",
	} {
		if !strings.Contains(out, marker) {
			t.Fatalf("expected stream to contain %q, got %s", marker, out)
		}
	}
}
