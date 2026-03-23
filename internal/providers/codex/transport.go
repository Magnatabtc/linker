package codex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"linker/internal/compat"
	"linker/internal/providers/shared"
	"linker/internal/state"
)

const (
	codexBackendBaseURL = "https://chatgpt.com/backend-api/codex"
	codexVersionHeader  = "0.101.0"
	codexUserAgent      = "codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"
)

func invoke(ctx context.Context, auth state.AccountAuth, req compat.NormalizedRequest) (compat.NormalizedResponse, state.AccountAuth, error) {
	endpoint := codexResponsesEndpoint(auth)
	auth = normalizeAuth(auth)
	req.Model = canonicalCodexModelName(req.Model)

	parsed, err := invokeCodexOnce(ctx, auth, endpoint, req)
	if err == nil {
		return parsed, auth, nil
	}
	if shouldRetryCodexMiniModel(err, req.Model) {
		retryReq := req
		retryReq.Model = "gpt-5.4"
		retryParsed, retryErr := invokeCodexOnce(ctx, auth, endpoint, retryReq)
		return retryParsed, auth, retryErr
	}
	return compat.NormalizedResponse{}, auth, err
}

func invokeCodexOnce(ctx context.Context, auth state.AccountAuth, endpoint string, req compat.NormalizedRequest) (compat.NormalizedResponse, error) {
	payload, err := buildCodexResponsesRequest(req)
	if err != nil {
		return compat.NormalizedResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return compat.NormalizedResponse{}, err
	}
	applyCodexHeaders(httpReq, auth)

	resp, err := shared.HTTPClient(2 * time.Minute).Do(httpReq)
	if err != nil {
		return compat.NormalizedResponse{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<22))
	if resp.StatusCode >= 300 {
		return compat.NormalizedResponse{}, &shared.HTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	return parseCodexResponsesResponse(body, req.Model)
}

func codexResponsesEndpoint(auth state.AccountAuth) string {
	if auth.Metadata != nil {
		if base := strings.TrimSpace(auth.Metadata["base_url"]); base != "" {
			return strings.TrimRight(base, "/") + "/responses/compact"
		}
	}
	base := strings.TrimSpace(auth.BaseURL)
	if base != "" && !strings.EqualFold(strings.TrimRight(base, "/"), strings.TrimRight(baseURL, "/")) {
		if strings.Contains(strings.ToLower(base), "chatgpt.com/backend-api/codex") {
			return strings.TrimRight(base, "/") + "/responses/compact"
		}
	}
	return codexBackendBaseURL + "/responses/compact"
}

func buildCodexResponsesRequest(req compat.NormalizedRequest) ([]byte, error) {
	input := make([]any, 0, len(req.Messages)+2)
	instructions := strings.TrimSpace(req.System)

	for _, message := range req.Messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			continue
		}
		if role == "system" {
			for _, part := range message.Parts {
				if part.Type != "text" {
					continue
				}
				if text := strings.TrimSpace(part.Text); text != "" {
					if instructions == "" {
						instructions = text
					} else {
						instructions += "\n" + text
					}
				}
			}
			continue
		}

		switch role {
		case "tool":
			for _, part := range message.Parts {
				if part.Type != "tool_result" {
					continue
				}
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": part.ToolID,
					"output":  part.Text,
				})
			}
			continue
		default:
			content := make([]codexContentPart, 0, len(message.Parts))
			functionCalls := make([]map[string]any, 0)
			for _, part := range message.Parts {
				switch part.Type {
				case "text":
					content = append(content, codexContentPart{
						Type: codexContentTypeForRole(role),
						Text: part.Text,
					})
				case "image":
					if url := codexImageURL(part.Source); url != "" {
						content = append(content, codexContentPart{
							Type:     "input_image",
							ImageURL: url,
						})
					}
				case "tool_use":
					callID := strings.TrimSpace(part.ToolID)
					name := strings.TrimSpace(part.Name)
					if callID == "" || name == "" {
						continue
					}
					functionCalls = append(functionCalls, map[string]any{
						"type":      "function_call",
						"call_id":   callID,
						"name":      name,
						"arguments": codexArgumentsJSON(part.Input),
					})
				}
			}
			if len(content) > 0 {
				input = append(input, codexMessageItem(role, content))
			}
			for _, functionCall := range functionCalls {
				input = append(input, functionCall)
			}
		}
	}

	tools := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		toolName := strings.TrimSpace(tool.Name)
		if toolName == "" {
			continue
		}
		toolMap := map[string]any{
			"type": "function",
			"name": toolName,
		}
		if description := strings.TrimSpace(tool.Description); description != "" {
			toolMap["description"] = description
		}
		if tool.InputSchema != nil {
			toolMap["parameters"] = tool.InputSchema
		}
		tools = append(tools, toolMap)
	}

	body := map[string]any{
		"model":        strings.TrimSpace(req.Model),
		"instructions": instructions,
		"input":        input,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	return json.Marshal(body)
}

func applyCodexHeaders(req *http.Request, auth state.AccountAuth) {
	if req == nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Connection", "Keep-Alive")
	req.Header.Set("Version", codexVersionHeader)
	req.Header.Set("User-Agent", codexUserAgent)
	req.Header.Set("Session_id", newCodexSessionID())

	token := strings.TrimSpace(auth.AccessToken)
	if token == "" {
		token = strings.TrimSpace(auth.APIKey)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	if strings.TrimSpace(auth.Provider) != "" {
		req.Header.Set("Originator", "codex_cli_rs")
	}
	if accountID := codexAccountID(auth); accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
	for key, value := range auth.Metadata {
		lower := strings.ToLower(strings.TrimSpace(key))
		if strings.HasPrefix(lower, "header:") {
			req.Header.Set(strings.TrimSpace(key[len("header:"):]), value)
		}
	}
}

func parseCodexResponsesResponse(body []byte, fallbackModel string) (compat.NormalizedResponse, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return compat.NormalizedResponse{}, fmt.Errorf("Codex response body was empty")
	}

	var envelope codexResponseEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return compat.NormalizedResponse{}, err
	}

	response := envelope.resolved()
	out := compat.NormalizedResponse{
		ID:         firstNonEmptyTrimmed(response.ID, envelope.ID),
		Model:      firstNonEmptyTrimmed(response.Model, envelope.Model, fallbackModel),
		StopReason: "stop",
		Usage: compat.Usage{
			InputTokens:  response.Usage.InputTokens,
			OutputTokens: response.Usage.OutputTokens,
		},
	}

	for _, item := range response.Output {
		switch item.Type {
		case "message":
			out.Parts = append(out.Parts, parseCodexMessageParts(item.Content)...)
		case "function_call":
			out.Parts = append(out.Parts, compat.ContentPart{
				Type:   "tool_use",
				ToolID: firstNonEmptyTrimmed(item.CallID, item.ID),
				Name:   item.Name,
				Input:  codexArgumentsMap(item.Arguments),
			})
			out.StopReason = "tool_calls"
		default:
			if item.Text != "" {
				out.Parts = append(out.Parts, compat.ContentPart{Type: "text", Text: item.Text})
			}
		}
	}

	if len(out.Parts) == 0 {
		out.Parts = append(out.Parts, compat.ContentPart{Type: "text", Text: ""})
	}
	return out, nil
}

func parseCodexMessageParts(parts []codexContentPart) []compat.ContentPart {
	out := make([]compat.ContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text", "input_text", "output_text":
			out = append(out, compat.ContentPart{Type: "text", Text: part.Text})
		case "input_image":
			if url := strings.TrimSpace(part.ImageURL); url != "" {
				out = append(out, compat.ContentPart{
					Type:   "image",
					Source: map[string]any{"url": url},
				})
			}
		}
	}
	return out
}

func codexMessageItem(role string, content []codexContentPart) map[string]any {
	item := map[string]any{
		"type":    "message",
		"role":    role,
		"content": content,
	}
	return item
}

func codexContentTypeForRole(role string) string {
	if role == "assistant" {
		return "output_text"
	}
	return "input_text"
}

func codexImageURL(source map[string]any) string {
	if len(source) == 0 {
		return ""
	}
	if raw, ok := source["url"]; ok {
		return strings.TrimSpace(fmt.Sprint(raw))
	}
	if raw, ok := source["image_url"]; ok {
		return strings.TrimSpace(fmt.Sprint(raw))
	}
	return ""
}

func codexArgumentsJSON(input map[string]any) string {
	if len(input) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func codexArgumentsMap(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		return parsed
	}
	var nested string
	if err := json.Unmarshal([]byte(trimmed), &nested); err == nil {
		if err := json.Unmarshal([]byte(nested), &parsed); err == nil {
			return parsed
		}
		return map[string]any{"text": nested}
	}
	return map[string]any{}
}

func codexAccountID(auth state.AccountAuth) string {
	if auth.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(auth.Metadata["account_id"])
}

func newCodexSessionID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("codex-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func shouldRetryCodexMiniModel(err error, requestedModel string) bool {
	if strings.TrimSpace(requestedModel) != "gpt-5.4-mini" {
		return false
	}
	var httpErr *shared.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusBadRequest {
		return false
	}
	body := strings.TrimSpace(httpErr.Body)
	if body == "" {
		return false
	}
	if codexBodyIndicatesMiniModelUnsupported(body) {
		return true
	}
	return false
}

func codexBodyIndicatesMiniModelUnsupported(body string) bool {
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "gpt-5.4-mini") && !strings.Contains(lower, "gpt-5-mini-codex") {
		return false
	}
	unsupportedPhrases := []string{
		"not supported",
		"unsupported",
		"does not support",
		"not available",
	}
	for _, phrase := range unsupportedPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	for _, candidate := range codexErrorDetailCandidates(body) {
		lowerCandidate := strings.ToLower(candidate)
		if !strings.Contains(lowerCandidate, "gpt-5.4-mini") && !strings.Contains(lowerCandidate, "gpt-5-mini-codex") {
			continue
		}
		for _, phrase := range unsupportedPhrases {
			if strings.Contains(lowerCandidate, phrase) {
				return true
			}
		}
	}
	return false
}

func codexErrorDetailCandidates(body string) []string {
	var raw any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil
	}
	var candidates []string
	codexCollectErrorStrings(raw, &candidates)
	return candidates
}

func codexCollectErrorStrings(value any, candidates *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "detail", "message", "error":
				codexCollectErrorStrings(child, candidates)
			}
		}
	case []any:
		for _, item := range typed {
			codexCollectErrorStrings(item, candidates)
		}
	case string:
		*candidates = append(*candidates, typed)
	}
}

type codexResponseEnvelope struct {
	Type     string              `json:"type"`
	ID       string              `json:"id"`
	Model    string              `json:"model"`
	Status   string              `json:"status"`
	Output   []codexOutputItem   `json:"output"`
	Usage    codexUsage          `json:"usage"`
	Response codexResponseObject `json:"response"`
}

func (e codexResponseEnvelope) resolved() codexResponseObject {
	if e.Response.hasData() {
		return e.Response
	}
	return codexResponseObject{
		ID:     e.ID,
		Model:  e.Model,
		Status: e.Status,
		Output: e.Output,
		Usage:  e.Usage,
	}
}

type codexResponseObject struct {
	ID     string            `json:"id"`
	Model  string            `json:"model"`
	Status string            `json:"status"`
	Output []codexOutputItem `json:"output"`
	Usage  codexUsage        `json:"usage"`
}

func (o codexResponseObject) hasData() bool {
	return strings.TrimSpace(o.ID) != "" || strings.TrimSpace(o.Model) != "" || strings.TrimSpace(o.Status) != "" || len(o.Output) > 0 || !o.Usage.isZero()
}

type codexOutputItem struct {
	Type      string             `json:"type"`
	ID        string             `json:"id"`
	CallID    string             `json:"call_id"`
	Name      string             `json:"name"`
	Arguments string             `json:"arguments"`
	Text      string             `json:"text"`
	Content   []codexContentPart `json:"content"`
}

type codexContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type codexUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func (u codexUsage) isZero() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.TotalTokens == 0
}
