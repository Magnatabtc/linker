package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"linker/internal/compat"
	"linker/internal/platform"
	"linker/internal/providerkit"
	"linker/internal/providers/shared"
	"linker/internal/state"
)

type Driver struct{}

func New() *Driver {
	return &Driver{}
}

var antigravityModelAliases = map[string]string{
	"gemini-3.1-pro":               "gemini-3.1-pro",
	"Gemini 3.1 Pro (High)":        "gemini-3.1-pro",
	"gemini-3-pro":                 "gemini-3-pro",
	"Gemini 3.1 Pro (Low)":         "gemini-3-pro",
	"gemini-3-flash":               "gemini-3-flash",
	"Gemini 3 Flash":               "gemini-3-flash",
	"claude-sonnet-4-6":            "claude-sonnet-4-6",
	"Claude Sonnet 4.6 (Thinking)": "claude-sonnet-4-6",
	"claude-opus-4-6":              "claude-opus-4-6",
	"Claude Opus 4.6 (Thinking)":   "claude-opus-4-6",
	"gpt-oss-120b":                 "gpt-oss-120b",
	"GPT-OSS 120B (Medium)":        "gpt-oss-120b",
}

func (d *Driver) Info() providerkit.Info {
	return providerkit.Info{
		ID:          "antigravity",
		DisplayName: "Antigravity",
		AuthKind:    "oauth",
		Models: []string{
			"Gemini 3.1 Pro (High)",
			"Gemini 3.1 Pro (Low)",
			"Gemini 3 Flash",
			"Claude Sonnet 4.6 (Thinking)",
			"Claude Opus 4.6 (Thinking)",
			"GPT-OSS 120B (Medium)",
		},
	}
}

func (d *Driver) Authenticate(ctx context.Context, ui providerkit.Interactive, existing *state.AccountAuth) (state.AccountAuth, error) {
	return authenticate(ctx, ui, existing)
}

func (d *Driver) Refresh(ctx context.Context, auth state.AccountAuth) (state.AccountAuth, bool, error) {
	return refresh(ctx, auth)
}

func (d *Driver) DiscoverModels(_ context.Context, auth state.AccountAuth) ([]providerkit.Model, error) {
	models := make([]providerkit.Model, 0, len(d.Info().Models))
	for _, name := range d.Info().Models {
		models = append(models, providerkit.Model{
			Provider: auth.Provider,
			Name:     name,
			Label:    fmt.Sprintf("%s [%s]", name, auth.Email),
		})
	}
	return models, nil
}

func antigravityModelID(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	if canonical, ok := antigravityModelAliases[trimmed]; ok {
		return canonical
	}
	return trimmed
}

func (d *Driver) Invoke(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest) (compat.NormalizedResponse, state.AccountAuth, error) {
	auth = normalizeAuth(auth)
	payload, err := buildRequest(auth, req)
	if err != nil {
		return compat.NormalizedResponse{}, auth, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return compat.NormalizedResponse{}, auth, err
	}

	endpoint := fmt.Sprintf("%s/%s:generateContent", apiEndpoint, apiVersion)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return compat.NormalizedResponse{}, auth, err
	}
	applyAPIHeaders(httpReq, auth.AccessToken, platform.Detect())

	resp, err := shared.HTTPClient(2 * time.Minute).Do(httpReq)
	if err != nil {
		return compat.NormalizedResponse{}, auth, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<22))
	if resp.StatusCode >= 300 {
		return compat.NormalizedResponse{}, auth, &shared.HTTPError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	parsed, err := parseResponse(data)
	return parsed, auth, err
}

func (d *Driver) StreamAnthropic(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest, w io.Writer) (state.AccountAuth, error) {
	return d.stream(ctx, auth, req, newAnthropicEmitter(w, req.Model))
}

func (d *Driver) StreamOpenAI(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest, w io.Writer) (state.AccountAuth, error) {
	return d.stream(ctx, auth, req, newOpenAIEmitter(w, req.Model))
}

func (d *Driver) stream(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest, emitter streamEmitter) (state.AccountAuth, error) {
	auth = normalizeAuth(auth)
	payload, err := buildRequest(auth, req)
	if err != nil {
		return auth, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return auth, err
	}

	endpoint := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse", apiEndpoint, apiVersion)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return auth, err
	}
	applyAPIHeaders(httpReq, auth.AccessToken, platform.Detect())

	resp, err := shared.HTTPClient(2 * time.Minute).Do(httpReq)
	if err != nil {
		return auth, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<22))
		return auth, &shared.HTTPError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	if err := emitter.Start(); err != nil {
		return auth, err
	}
	if err := readStream(resp.Body, emitter); err != nil {
		return auth, err
	}
	return auth, emitter.Finish()
}

func buildRequest(auth state.AccountAuth, req compat.NormalizedRequest) (map[string]any, error) {
	if strings.TrimSpace(auth.ProjectID) == "" {
		return nil, fmt.Errorf("Antigravity account is missing a project id")
	}

	toolNames := map[string]string{}
	contents := make([]map[string]any, 0, len(req.Messages))
	for _, message := range req.Messages {
		switch message.Role {
		case "tool":
			parts := make([]map[string]any, 0, len(message.Parts))
			for _, part := range message.Parts {
				if part.Type != "tool_result" {
					continue
				}
				parts = append(parts, map[string]any{
					"functionResponse": map[string]any{
						"id":   part.ToolID,
						"name": firstNonEmpty(toolNames[part.ToolID], part.ToolID),
						"response": map[string]any{
							"result": part.Text,
						},
					},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, map[string]any{
					"role":  "user",
					"parts": parts,
				})
			}
		default:
			role := message.Role
			if role == "assistant" {
				role = "model"
			}
			parts := make([]map[string]any, 0, len(message.Parts))
			for _, part := range message.Parts {
				switch part.Type {
				case "text":
					parts = append(parts, map[string]any{"text": part.Text})
				case "image":
					inlineData := buildInlineData(part.Source)
					if len(inlineData) > 0 {
						parts = append(parts, map[string]any{"inlineData": inlineData})
					}
				case "tool_use":
					toolNames[part.ToolID] = part.Name
					parts = append(parts, map[string]any{
						"functionCall": map[string]any{
							"id":   part.ToolID,
							"name": part.Name,
							"args": compatDefaultMap(part.Input),
						},
					})
				}
			}
			if len(parts) > 0 {
				contents = append(contents, map[string]any{
					"role":  role,
					"parts": parts,
				})
			}
		}
	}

	request := map[string]any{
		"contents": contents,
	}
	if req.System != "" {
		request["systemInstruction"] = map[string]any{
			"role": "user",
			"parts": []map[string]any{{
				"text": req.System,
			}},
		}
	}
	if req.MaxTokens > 0 {
		request["generationConfig"] = map[string]any{
			"maxOutputTokens": req.MaxTokens,
		}
	}
	if len(req.Tools) > 0 {
		declarations := make([]map[string]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			declarations = append(declarations, map[string]any{
				"name":                 tool.Name,
				"description":          tool.Description,
				"parametersJsonSchema": tool.InputSchema,
			})
		}
		request["tools"] = []map[string]any{{
			"functionDeclarations": declarations,
		}}
	}

	return map[string]any{
		"project":   auth.ProjectID,
		"model":     antigravityModelID(req.Model),
		"request":   request,
		"userAgent": buildUserAgent(),
		"requestId": fmt.Sprintf("agent-%d", time.Now().UnixNano()),
	}, nil
}

func parseResponse(body []byte) (compat.NormalizedResponse, error) {
	var raw struct {
		Response struct {
			ResponseID   string `json:"responseId"`
			ModelVersion string `json:"modelVersion"`
			Candidates   []struct {
				FinishReason string `json:"finishReason"`
				Content      struct {
					Parts []struct {
						Text         string `json:"text"`
						Thought      bool   `json:"thought"`
						FunctionCall struct {
							ID   string         `json:"id"`
							Name string         `json:"name"`
							Args map[string]any `json:"args"`
						} `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
			UsageMetadata struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
			} `json:"usageMetadata"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return compat.NormalizedResponse{}, err
	}
	if len(raw.Response.Candidates) == 0 {
		return compat.NormalizedResponse{}, fmt.Errorf("Antigravity returned no candidates")
	}
	resp := compat.NormalizedResponse{
		ID:         raw.Response.ResponseID,
		Model:      firstNonEmpty(raw.Response.ModelVersion, ""),
		StopReason: mapFinishReason(raw.Response.Candidates[0].FinishReason),
		Usage: compat.Usage{
			InputTokens:  raw.Response.UsageMetadata.PromptTokenCount,
			OutputTokens: raw.Response.UsageMetadata.CandidatesTokenCount + raw.Response.UsageMetadata.ThoughtsTokenCount,
		},
	}
	for idx, part := range raw.Response.Candidates[0].Content.Parts {
		if part.FunctionCall.Name != "" {
			resp.Parts = append(resp.Parts, compat.ContentPart{
				Type:   "tool_use",
				ToolID: firstNonEmpty(part.FunctionCall.ID, fmt.Sprintf("tool_%d", idx)),
				Name:   part.FunctionCall.Name,
				Input:  compatDefaultMap(part.FunctionCall.Args),
			})
			continue
		}
		if part.Thought || part.Text == "" {
			continue
		}
		resp.Parts = append(resp.Parts, compat.ContentPart{Type: "text", Text: part.Text})
	}
	if len(resp.Parts) == 0 {
		resp.Parts = append(resp.Parts, compat.ContentPart{Type: "text", Text: ""})
	}
	return resp, nil
}

type streamChunk struct {
	Response struct {
		ResponseID   string `json:"responseId"`
		ModelVersion string `json:"modelVersion"`
		Candidates   []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall struct {
						ID   string         `json:"id"`
						Name string         `json:"name"`
						Args map[string]any `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
		} `json:"usageMetadata"`
	} `json:"response"`
}

type streamEmitter interface {
	Start() error
	Emit(chunk streamChunk) error
	Finish() error
}

func readStream(body io.Reader, emitter streamEmitter) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return err
		}
		if err := emitter.Emit(chunk); err != nil {
			return err
		}
	}
	return scanner.Err()
}

type anthropicEmitter struct {
	writer     *bufio.Writer
	model      string
	messageID  string
	started    bool
	textOpen   bool
	textIndex  int
	nextIndex  int
	stopReason string
	usage      compat.Usage
}

func newAnthropicEmitter(w io.Writer, model string) *anthropicEmitter {
	writer := bufio.NewWriter(w)
	return &anthropicEmitter{writer: writer, model: model, textIndex: -1, nextIndex: 0}
}

func (e *anthropicEmitter) Start() error { return nil }

func (e *anthropicEmitter) Emit(chunk streamChunk) error {
	if !e.started {
		e.messageID = firstNonEmpty(chunk.Response.ResponseID, fmt.Sprintf("msg_linker_%d", time.Now().UnixNano()))
		e.model = firstNonEmpty(chunk.Response.ModelVersion, e.model)
		if err := writeSSE(e.writer, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            e.messageID,
				"type":          "message",
				"role":          "assistant",
				"model":         e.model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  chunk.Response.UsageMetadata.PromptTokenCount,
					"output_tokens": 0,
				},
			},
		}); err != nil {
			return err
		}
		e.started = true
	}
	if len(chunk.Response.Candidates) == 0 {
		return nil
	}
	candidate := chunk.Response.Candidates[0]
	e.stopReason = mapFinishReason(candidate.FinishReason)
	e.usage = compat.Usage{
		InputTokens:  chunk.Response.UsageMetadata.PromptTokenCount,
		OutputTokens: chunk.Response.UsageMetadata.CandidatesTokenCount + chunk.Response.UsageMetadata.ThoughtsTokenCount,
	}
	for _, part := range candidate.Content.Parts {
		switch {
		case part.FunctionCall.Name != "":
			if e.textOpen {
				if err := writeSSE(e.writer, "content_block_stop", map[string]any{"type": "content_block_stop", "index": e.textIndex}); err != nil {
					return err
				}
				e.textOpen = false
			}
			index := e.nextIndex
			e.nextIndex++
			if err := writeSSE(e.writer, "content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": index,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    firstNonEmpty(part.FunctionCall.ID, fmt.Sprintf("tool_linker_%d", index)),
					"name":  part.FunctionCall.Name,
					"input": map[string]any{},
				},
			}); err != nil {
				return err
			}
			args, _ := json.Marshal(compatDefaultMap(part.FunctionCall.Args))
			if err := writeSSE(e.writer, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": index,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": string(args),
				},
			}); err != nil {
				return err
			}
			if err := writeSSE(e.writer, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index}); err != nil {
				return err
			}
		case part.Text != "":
			if !e.textOpen {
				e.textIndex = e.nextIndex
				e.nextIndex++
				if err := writeSSE(e.writer, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": e.textIndex,
					"content_block": map[string]any{
						"type": "text",
						"text": "",
					},
				}); err != nil {
					return err
				}
				e.textOpen = true
			}
			if err := writeSSE(e.writer, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": e.textIndex,
				"delta": map[string]any{
					"type": "text_delta",
					"text": part.Text,
				},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *anthropicEmitter) Finish() error {
	if e.textOpen {
		if err := writeSSE(e.writer, "content_block_stop", map[string]any{"type": "content_block_stop", "index": e.textIndex}); err != nil {
			return err
		}
	}
	if err := writeSSE(e.writer, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason": firstNonEmpty(e.stopReason, "end_turn"),
		},
		"usage": map[string]any{
			"output_tokens": e.usage.OutputTokens,
		},
	}); err != nil {
		return err
	}
	if err := writeSSE(e.writer, "message_stop", map[string]any{"type": "message_stop"}); err != nil {
		return err
	}
	return e.writer.Flush()
}

type openAIEmitter struct {
	writer     *bufio.Writer
	model      string
	messageID  string
	started    bool
	stopReason string
}

func newOpenAIEmitter(w io.Writer, model string) *openAIEmitter {
	return &openAIEmitter{writer: bufio.NewWriter(w), model: model}
}

func (e *openAIEmitter) Start() error { return nil }

func (e *openAIEmitter) Emit(chunk streamChunk) error {
	if !e.started {
		e.messageID = firstNonEmpty(chunk.Response.ResponseID, fmt.Sprintf("chatcmpl-linker-%d", time.Now().UnixNano()))
		e.model = firstNonEmpty(chunk.Response.ModelVersion, e.model)
		if err := writeSSE(e.writer, "", map[string]any{
			"id":      e.messageID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   e.model,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{"role": "assistant"},
			}},
		}); err != nil {
			return err
		}
		e.started = true
	}
	if len(chunk.Response.Candidates) == 0 {
		return nil
	}
	candidate := chunk.Response.Candidates[0]
	e.stopReason = mapOpenAIFinishReason(mapFinishReason(candidate.FinishReason))
	for _, part := range candidate.Content.Parts {
		switch {
		case part.FunctionCall.Name != "":
			args, _ := json.Marshal(compatDefaultMap(part.FunctionCall.Args))
			if err := writeSSE(e.writer, "", map[string]any{
				"id":      e.messageID,
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   e.model,
				"choices": []map[string]any{{
					"index": 0,
					"delta": map[string]any{
						"tool_calls": []map[string]any{{
							"index": 0,
							"id":    firstNonEmpty(part.FunctionCall.ID, "call_linker"),
							"type":  "function",
							"function": map[string]any{
								"name":      part.FunctionCall.Name,
								"arguments": string(args),
							},
						}},
					},
				}},
			}); err != nil {
				return err
			}
		case part.Text != "":
			if err := writeSSE(e.writer, "", map[string]any{
				"id":      e.messageID,
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   e.model,
				"choices": []map[string]any{{
					"index": 0,
					"delta": map[string]any{"content": part.Text},
				}},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *openAIEmitter) Finish() error {
	if err := writeSSE(e.writer, "", map[string]any{
		"id":      e.messageID,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   e.model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": firstNonEmpty(e.stopReason, "stop"),
		}},
	}); err != nil {
		return err
	}
	if _, err := e.writer.WriteString("data: [DONE]\n\n"); err != nil {
		return err
	}
	return e.writer.Flush()
}

func writeSSE(w *bufio.Writer, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := w.WriteString("event: " + event + "\n"); err != nil {
			return err
		}
	}
	if _, err := w.WriteString("data: " + string(data) + "\n\n"); err != nil {
		return err
	}
	return w.Flush()
}

func applyAPIHeaders(req *http.Request, accessToken string, env platform.Environment) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", buildUserAgent())
	req.Header.Set("X-Goog-Api-Client", googAPIClient)
	req.Header.Set("Client-Metadata", fmt.Sprintf(`{"ideType":"ANTIGRAVITY","platform":"%s","pluginType":"GEMINI"}`, platformName(env.OS)))
}

func buildUserAgent() string {
	return fmt.Sprintf("antigravity/1.15.8 %s/%s", runtime.GOOS, runtime.GOARCH)
}

func platformName(goos string) string {
	switch goos {
	case "darwin":
		return "MACOS"
	case "windows":
		return "WINDOWS"
	default:
		return "LINUX"
	}
}

func mapFinishReason(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "MAX_TOKENS":
		return "max_tokens"
	case "STOP", "FINISH_REASON_UNSPECIFIED", "":
		return "end_turn"
	default:
		return "end_turn"
	}
}

func buildInlineData(source map[string]any) map[string]any {
	if len(source) == 0 {
		return nil
	}
	if strings.TrimSpace(fmt.Sprintf("%v", source["type"])) != "base64" {
		return nil
	}
	data := strings.TrimSpace(fmt.Sprintf("%v", source["data"]))
	if data == "" {
		return nil
	}
	return map[string]any{
		"mimeType": strings.TrimSpace(fmt.Sprintf("%v", source["media_type"])),
		"data":     data,
	}
}

func mapOpenAIFinishReason(stopReason string) string {
	switch strings.TrimSpace(stopReason) {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	default:
		return "stop"
	}
}

func compatDefaultMap(value map[string]any) map[string]any {
	if value != nil {
		return value
	}
	return map[string]any{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
