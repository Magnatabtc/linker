package modelnorm

import "strings"

var aliases = map[string]string{
	"gpt-5-codex":                  "gpt-5.4",
	"gpt 5 codex":                  "gpt-5.4",
	"gpt-5.4":                      "gpt-5.4",
	"gpt 5.4":                      "gpt-5.4",
	"gpt-5-mini-codex":             "gpt-5.4-mini",
	"gpt 5 mini codex":             "gpt-5.4-mini",
	"gpt-5.4-mini":                 "gpt-5.4-mini",
	"gpt 5.4 mini":                 "gpt-5.4-mini",
	"gemini 3.1 pro (high)":        "gemini-3.1-pro",
	"gemini 3.1 pro (low)":         "gemini-3-pro",
	"gemini 3 flash":               "gemini-3-flash",
	"claude sonnet 4.6 (thinking)": "claude-sonnet-4-6",
	"claude opus 4.6 (thinking)":   "claude-opus-4-6",
	"gpt-oss 120b (medium)":        "gpt-oss-120b",
}

func Normalize(providerID string, name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	if alias, ok := aliases[strings.ToLower(trimmed)]; ok {
		return alias
	}
	return trimmed
}

func Same(providerID string, left string, right string) bool {
	return strings.EqualFold(Normalize(providerID, left), Normalize(providerID, right))
}
