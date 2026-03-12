package compat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type ContentPart struct {
	Type    string         `json:"type"`
	Text    string         `json:"text,omitempty"`
	Name    string         `json:"name,omitempty"`
	ToolID  string         `json:"tool_id,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
	Source  map[string]any `json:"source,omitempty"`
	IsError bool           `json:"is_error,omitempty"`
}

type Message struct {
	Role  string        `json:"role"`
	Parts []ContentPart `json:"parts"`
}

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

type NormalizedRequest struct {
	Model     string    `json:"model"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
	Tools     []Tool    `json:"tools,omitempty"`
	Stream    bool      `json:"stream"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

type NormalizedResponse struct {
	ID         string        `json:"id"`
	Model      string        `json:"model"`
	Parts      []ContentPart `json:"parts"`
	StopReason string        `json:"stop_reason,omitempty"`
	Usage      Usage         `json:"usage,omitempty"`
}

func (r NormalizedResponse) Text() string {
	var builder strings.Builder
	for _, part := range r.Parts {
		if part.Type == "text" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func ParseAnthropicRequest(body io.Reader) (NormalizedRequest, error) {
	var raw struct {
		Model    string `json:"model"`
		System   any    `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema any    `json:"input_schema"`
		} `json:"tools"`
		MaxTokens int  `json:"max_tokens"`
		Stream    bool `json:"stream"`
	}
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		return NormalizedRequest{}, err
	}
	req := NormalizedRequest{
		Model:     raw.Model,
		System:    flattenText(raw.System),
		Stream:    raw.Stream,
		MaxTokens: raw.MaxTokens,
	}
	for _, tool := range raw.Tools {
		req.Tools = append(req.Tools, Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	for _, message := range raw.Messages {
		req.Messages = append(req.Messages, parseAnthropicMessage(message.Role, message.Content)...)
	}
	return req, nil
}

func ParseOpenAIRequest(body io.Reader) (NormalizedRequest, error) {
	var raw struct {
		Model    string `json:"model"`
		Messages []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Parameters  any    `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		Stream              bool `json:"stream"`
		MaxTokens           int  `json:"max_tokens"`
		MaxCompletionTokens int  `json:"max_completion_tokens"`
	}
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		return NormalizedRequest{}, err
	}
	req := NormalizedRequest{
		Model:     raw.Model,
		Stream:    raw.Stream,
		MaxTokens: raw.MaxCompletionTokens,
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = raw.MaxTokens
	}
	for _, tool := range raw.Tools {
		req.Tools = append(req.Tools, Tool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}
	for _, message := range raw.Messages {
		switch message.Role {
		case "system":
			req.System = joinText(parseOpenAIContent(message.Content))
		case "tool":
			req.Messages = append(req.Messages, Message{
				Role: "tool",
				Parts: []ContentPart{{
					Type:   "tool_result",
					ToolID: message.ToolCallID,
					Text:   joinText(parseOpenAIContent(message.Content)),
				}},
			})
		default:
			parts := parseOpenAIContent(message.Content)
			for _, toolCall := range message.ToolCalls {
				parts = append(parts, ContentPart{
					Type:   "tool_use",
					Name:   toolCall.Function.Name,
					ToolID: toolCall.ID,
					Input:  parseJSONMap(toolCall.Function.Arguments),
				})
			}
			req.Messages = append(req.Messages, Message{Role: message.Role, Parts: parts})
		}
	}
	return req, nil
}

func WriteAnthropicResponse(w io.Writer, resp NormalizedResponse) error {
	type contentBlock struct {
		Type      string         `json:"type"`
		Text      string         `json:"text,omitempty"`
		ID        string         `json:"id,omitempty"`
		Name      string         `json:"name,omitempty"`
		Input     map[string]any `json:"input,omitempty"`
		ToolUseID string         `json:"tool_use_id,omitempty"`
		Content   any            `json:"content,omitempty"`
		IsError   bool           `json:"is_error,omitempty"`
	}
	payload := struct {
		ID           string         `json:"id"`
		Type         string         `json:"type"`
		Role         string         `json:"role"`
		Model        string         `json:"model"`
		Content      []contentBlock `json:"content"`
		StopReason   string         `json:"stop_reason"`
		StopSequence any            `json:"stop_sequence"`
		Usage        Usage          `json:"usage"`
	}{
		ID:           ensureID(resp.ID, "msg_linker"),
		Type:         "message",
		Role:         "assistant",
		Model:        resp.Model,
		StopReason:   anthropicStopReason(resp),
		StopSequence: nil,
		Usage:        resp.Usage,
	}
	for _, part := range resp.Parts {
		switch part.Type {
		case "text":
			payload.Content = append(payload.Content, contentBlock{Type: "text", Text: part.Text})
		case "tool_use":
			payload.Content = append(payload.Content, contentBlock{
				Type:  "tool_use",
				ID:    ensureID(part.ToolID, "tool_linker"),
				Name:  part.Name,
				Input: defaultMap(part.Input),
			})
		}
	}
	return json.NewEncoder(w).Encode(payload)
}

func WriteAnthropicStream(w io.Writer, resp NormalizedResponse) error {
	bw := bufio.NewWriter(w)
	start := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            ensureID(resp.ID, "msg_linker"),
			"type":          "message",
			"role":          "assistant",
			"model":         resp.Model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  resp.Usage.InputTokens,
				"output_tokens": 0,
			},
		},
	}
	if err := sse(bw, "message_start", start); err != nil {
		return err
	}
	for index, part := range resp.Parts {
		block := anthropicContentBlock(part, index)
		if err := sse(bw, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         index,
			"content_block": block,
		}); err != nil {
			return err
		}
		switch part.Type {
		case "text":
			for _, chunk := range chunk(part.Text, 64) {
				if err := sse(bw, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": index,
					"delta": map[string]any{
						"type": "text_delta",
						"text": chunk,
					},
				}); err != nil {
					return err
				}
			}
		case "tool_use":
			input, _ := json.Marshal(defaultMap(part.Input))
			if err := sse(bw, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": index,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": string(input),
				},
			}); err != nil {
				return err
			}
		}
		if err := sse(bw, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": index,
		}); err != nil {
			return err
		}
	}
	if err := sse(bw, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason": anthropicStopReason(resp),
		},
		"usage": map[string]any{
			"output_tokens": resp.Usage.OutputTokens,
		},
	}); err != nil {
		return err
	}
	if err := sse(bw, "message_stop", map[string]any{"type": "message_stop"}); err != nil {
		return err
	}
	return bw.Flush()
}

func WriteOpenAIResponse(w io.Writer, resp NormalizedResponse) error {
	toolCalls := []map[string]any{}
	for _, part := range resp.Parts {
		if part.Type != "tool_use" {
			continue
		}
		args, _ := json.Marshal(defaultMap(part.Input))
		toolCalls = append(toolCalls, map[string]any{
			"id":   ensureID(part.ToolID, "call_linker"),
			"type": "function",
			"function": map[string]any{
				"name":      part.Name,
				"arguments": string(args),
			},
		})
	}
	message := map[string]any{
		"role":    "assistant",
		"content": resp.Text(),
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	payload := map[string]any{
		"id":      ensureID(resp.ID, "chatcmpl-linker"),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   resp.Model,
		"choices": []map[string]any{{
			"index":         0,
			"finish_reason": openAIFinishReason(resp),
			"message":       message,
		}},
		"usage": map[string]any{
			"prompt_tokens":     resp.Usage.InputTokens,
			"completion_tokens": resp.Usage.OutputTokens,
			"total_tokens":      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
	return json.NewEncoder(w).Encode(payload)
}

func WriteOpenAIStream(w io.Writer, resp NormalizedResponse) error {
	bw := bufio.NewWriter(w)
	base := map[string]any{
		"id":      ensureID(resp.ID, "chatcmpl-linker"),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   resp.Model,
	}
	first := cloneMap(base)
	first["choices"] = []map[string]any{{
		"index": 0,
		"delta": map[string]any{"role": "assistant"},
	}}
	if err := sse(bw, "", first); err != nil {
		return err
	}
	for _, part := range resp.Parts {
		switch part.Type {
		case "text":
			for _, value := range chunk(part.Text, 64) {
				frame := cloneMap(base)
				frame["choices"] = []map[string]any{{
					"index": 0,
					"delta": map[string]any{"content": value},
				}}
				if err := sse(bw, "", frame); err != nil {
					return err
				}
			}
		case "tool_use":
			args, _ := json.Marshal(defaultMap(part.Input))
			frame := cloneMap(base)
			frame["choices"] = []map[string]any{{
				"index": 0,
				"delta": map[string]any{
					"tool_calls": []map[string]any{{
						"index": 0,
						"id":    ensureID(part.ToolID, "call_linker"),
						"type":  "function",
						"function": map[string]any{
							"name":      part.Name,
							"arguments": string(args),
						},
					}},
				},
			}}
			if err := sse(bw, "", frame); err != nil {
				return err
			}
		}
	}
	last := cloneMap(base)
	last["choices"] = []map[string]any{{
		"index":         0,
		"delta":         map[string]any{},
		"finish_reason": openAIFinishReason(resp),
	}}
	if err := sse(bw, "", last); err != nil {
		return err
	}
	if _, err := bw.WriteString("data: [DONE]\n\n"); err != nil {
		return err
	}
	return bw.Flush()
}

func BuildOpenAIUpstreamRequest(req NormalizedRequest) (map[string]any, error) {
	messages := make([]map[string]any, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.System})
	}
	for _, message := range req.Messages {
		switch message.Role {
		case "tool":
			for _, part := range message.Parts {
				if part.Type != "tool_result" {
					continue
				}
				messages = append(messages, map[string]any{
					"role":         "tool",
					"tool_call_id": part.ToolID,
					"content":      part.Text,
				})
			}
		default:
			payload := map[string]any{"role": message.Role}
			contentParts := make([]map[string]any, 0, len(message.Parts))
			toolCalls := make([]map[string]any, 0)
			for _, part := range message.Parts {
				switch part.Type {
				case "text":
					contentParts = append(contentParts, map[string]any{"type": "text", "text": part.Text})
				case "image":
					contentParts = append(contentParts, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": fmt.Sprintf("%v", defaultMap(part.Source)["url"])},
					})
				case "tool_use":
					args, _ := json.Marshal(defaultMap(part.Input))
					toolCalls = append(toolCalls, map[string]any{
						"id":   ensureID(part.ToolID, "call_linker"),
						"type": "function",
						"function": map[string]any{
							"name":      part.Name,
							"arguments": string(args),
						},
					})
				}
			}
			payload["content"] = openAIContentValue(contentParts)
			if len(toolCalls) > 0 {
				payload["tool_calls"] = toolCalls
			}
			messages = append(messages, payload)
		}
	}
	body := map[string]any{
		"model":    req.Model,
		"messages": messages,
	}
	if req.MaxTokens > 0 {
		body["max_completion_tokens"] = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  tool.InputSchema,
				},
			})
		}
		body["tools"] = tools
	}
	return body, nil
}

func ParseOpenAIUpstreamResponse(body io.Reader) (NormalizedResponse, error) {
	var raw struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   any `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		return NormalizedResponse{}, err
	}
	if len(raw.Choices) == 0 {
		return NormalizedResponse{}, errors.New("upstream returned no choices")
	}
	resp := NormalizedResponse{
		ID:         raw.ID,
		Model:      raw.Model,
		StopReason: raw.Choices[0].FinishReason,
		Usage: Usage{
			InputTokens:  raw.Usage.PromptTokens,
			OutputTokens: raw.Usage.CompletionTokens,
		},
	}
	resp.Parts = append(resp.Parts, parseOpenAIContent(raw.Choices[0].Message.Content)...)
	for _, call := range raw.Choices[0].Message.ToolCalls {
		resp.Parts = append(resp.Parts, ContentPart{
			Type:   "tool_use",
			Name:   call.Function.Name,
			ToolID: call.ID,
			Input:  parseJSONMap(call.Function.Arguments),
		})
	}
	if len(resp.Parts) == 0 {
		resp.Parts = append(resp.Parts, ContentPart{Type: "text", Text: ""})
	}
	return resp, nil
}

func BuildAnthropicUpstreamRequest(req NormalizedRequest) (map[string]any, error) {
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, message := range req.Messages {
		switch message.Role {
		case "tool":
			content := make([]map[string]any, 0, len(message.Parts))
			for _, part := range message.Parts {
				if part.Type != "tool_result" {
					continue
				}
				content = append(content, map[string]any{
					"type":        "tool_result",
					"tool_use_id": part.ToolID,
					"content":     part.Text,
					"is_error":    part.IsError,
				})
			}
			if len(content) > 0 {
				messages = append(messages, map[string]any{
					"role":    "user",
					"content": content,
				})
			}
		default:
			content := make([]map[string]any, 0, len(message.Parts))
			for _, part := range message.Parts {
				switch part.Type {
				case "text":
					content = append(content, map[string]any{"type": "text", "text": part.Text})
				case "image":
					content = append(content, map[string]any{"type": "image", "source": defaultMap(part.Source)})
				case "tool_use":
					content = append(content, map[string]any{
						"type":  "tool_use",
						"id":    ensureID(part.ToolID, "tool_linker"),
						"name":  part.Name,
						"input": defaultMap(part.Input),
					})
				}
			}
			messages = append(messages, map[string]any{
				"role":    message.Role,
				"content": content,
			})
		}
	}
	body := map[string]any{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": anthropicMaxTokens(req.MaxTokens),
	}
	if req.System != "" {
		body["system"] = req.System
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, map[string]any{
				"name":         tool.Name,
				"description":  tool.Description,
				"input_schema": tool.InputSchema,
			})
		}
		body["tools"] = tools
	}
	return body, nil
}

func ParseAnthropicUpstreamResponse(body io.Reader) (NormalizedResponse, error) {
	var raw struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
		Usage Usage `json:"usage"`
	}
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		return NormalizedResponse{}, err
	}
	resp := NormalizedResponse{
		ID:         raw.ID,
		Model:      raw.Model,
		StopReason: raw.StopReason,
		Usage:      raw.Usage,
	}
	for _, part := range raw.Content {
		switch part.Type {
		case "text":
			resp.Parts = append(resp.Parts, ContentPart{Type: "text", Text: part.Text})
		case "tool_use":
			resp.Parts = append(resp.Parts, ContentPart{
				Type:   "tool_use",
				ToolID: part.ID,
				Name:   part.Name,
				Input:  part.Input,
			})
		}
	}
	if len(resp.Parts) == 0 {
		resp.Parts = append(resp.Parts, ContentPart{Type: "text", Text: ""})
	}
	return resp, nil
}

func parseAnthropicMessage(role string, content any) []Message {
	parts := parseParts(content)
	messages := []Message{}
	normal := []ContentPart{}
	for _, part := range parts {
		if part.Type == "tool_result" {
			messages = append(messages, Message{Role: "tool", Parts: []ContentPart{part}})
			continue
		}
		normal = append(normal, part)
	}
	if len(normal) > 0 {
		messages = append(messages, Message{Role: role, Parts: normal})
	}
	return messages
}

func parseParts(content any) []ContentPart {
	switch value := content.(type) {
	case string:
		return []ContentPart{{Type: "text", Text: value}}
	case []any:
		parts := make([]ContentPart, 0, len(value))
		for _, item := range value {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch blockType, _ := block["type"].(string); blockType {
			case "text":
				parts = append(parts, ContentPart{Type: "text", Text: fmt.Sprintf("%v", block["text"])})
			case "image":
				parts = append(parts, ContentPart{Type: "image", Source: defaultMap(block["source"])})
			case "tool_use":
				parts = append(parts, ContentPart{
					Type:   "tool_use",
					Name:   fmt.Sprintf("%v", block["name"]),
					ToolID: fmt.Sprintf("%v", block["id"]),
					Input:  defaultMap(block["input"]),
				})
			case "tool_result":
				parts = append(parts, ContentPart{
					Type:    "tool_result",
					ToolID:  fmt.Sprintf("%v", block["tool_use_id"]),
					Text:    flattenText(block["content"]),
					IsError: asBool(block["is_error"]),
				})
			}
		}
		return parts
	default:
		return nil
	}
}

func parseOpenAIContent(content any) []ContentPart {
	switch value := content.(type) {
	case string:
		return []ContentPart{{Type: "text", Text: value}}
	case []any:
		parts := make([]ContentPart, 0, len(value))
		for _, item := range value {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch blockType, _ := block["type"].(string); blockType {
			case "text", "input_text":
				text := fmt.Sprintf("%v", block["text"])
				if text == "" {
					text = fmt.Sprintf("%v", block["content"])
				}
				parts = append(parts, ContentPart{Type: "text", Text: text})
			case "image_url":
				parts = append(parts, ContentPart{Type: "image", Source: defaultMap(block["image_url"])})
			case "input_image":
				parts = append(parts, ContentPart{Type: "image", Source: map[string]any{"url": block["image_url"]}})
			}
		}
		return parts
	default:
		return nil
	}
}

func flattenText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return joinText(parseParts(value))
	}
}

func joinText(parts []ContentPart) string {
	var builder strings.Builder
	for _, part := range parts {
		if part.Type == "text" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func parseJSONMap(value string) map[string]any {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return map[string]any{}
	}
	return parsed
}

func ensureID(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func anthropicStopReason(resp NormalizedResponse) string {
	for _, part := range resp.Parts {
		if part.Type == "tool_use" {
			return "tool_use"
		}
	}
	switch resp.StopReason {
	case "", "stop", "end_turn":
		return "end_turn"
	case "tool_calls", "tool_use":
		return "tool_use"
	default:
		return resp.StopReason
	}
}

func openAIFinishReason(resp NormalizedResponse) string {
	for _, part := range resp.Parts {
		if part.Type == "tool_use" {
			return "tool_calls"
		}
	}
	switch resp.StopReason {
	case "", "end_turn", "stop":
		return "stop"
	case "max_tokens":
		return "length"
	default:
		return resp.StopReason
	}
}

func anthropicMaxTokens(value int) int {
	if value > 0 {
		return value
	}
	return 1024
}

func chunk(value string, size int) []string {
	if value == "" {
		return []string{""}
	}
	result := []string{}
	for len(value) > size {
		result = append(result, value[:size])
		value = value[size:]
	}
	if value != "" {
		result = append(result, value)
	}
	return result
}

func sse(w *bufio.Writer, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := w.WriteString("event: " + event + "\n"); err != nil {
			return err
		}
	}
	if _, err := w.WriteString("data: "); err != nil {
		return err
	}
	if _, err := io.Copy(w, bytes.NewReader(data)); err != nil {
		return err
	}
	if _, err := w.WriteString("\n\n"); err != nil {
		return err
	}
	return w.Flush()
}

func defaultMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok && typed != nil {
		return typed
	}
	return map[string]any{}
}

func asBool(value any) bool {
	typed, _ := value.(bool)
	return typed
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func anthropicContentBlock(part ContentPart, index int) map[string]any {
	switch part.Type {
	case "tool_use":
		return map[string]any{
			"type":  "tool_use",
			"id":    ensureID(part.ToolID, fmt.Sprintf("tool_%d", index)),
			"name":  part.Name,
			"input": map[string]any{},
		}
	default:
		return map[string]any{
			"type": "text",
			"text": "",
		}
	}
}

func openAIContentValue(parts []map[string]any) any {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		if text, ok := parts[0]["text"].(string); ok && parts[0]["type"] == "text" {
			return text
		}
	}
	return parts
}
