package antigravity

import (
	"reflect"
	"testing"

	"linker/internal/compat"
	"linker/internal/state"
)

func TestInfoAdvertisesCurrentAntigravityModels(t *testing.T) {
	t.Parallel()

	info := New().Info()
	want := []string{
		"Gemini 3.1 Pro (High)",
		"Gemini 3.1 Pro (Low)",
		"Gemini 3 Flash",
		"Claude Sonnet 4.6 (Thinking)",
		"Claude Opus 4.6 (Thinking)",
		"GPT-OSS 120B (Medium)",
	}

	if !reflect.DeepEqual(info.Models, want) {
		t.Fatalf("models = %#v, want %#v", info.Models, want)
	}
	if info.ID != "antigravity" {
		t.Fatalf("id = %q, want %q", info.ID, "antigravity")
	}
	if info.DisplayName != "Antigravity" {
		t.Fatalf("display name = %q, want %q", info.DisplayName, "Antigravity")
	}
	if info.AuthKind != "oauth" {
		t.Fatalf("auth kind = %q, want %q", info.AuthKind, "oauth")
	}
}

func TestBuildRequestCanonicalizesAntigravityModelNames(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Gemini 3.1 Pro (High)":        "gemini-3.1-pro",
		"Gemini 3.1 Pro (Low)":         "gemini-3-pro",
		"Gemini 3 Flash":               "gemini-3-flash",
		"Claude Sonnet 4.6 (Thinking)": "claude-sonnet-4-6",
		"Claude Opus 4.6 (Thinking)":   "claude-opus-4-6",
		"GPT-OSS 120B (Medium)":        "gpt-oss-120b",
		"gemini-3.1-pro":               "gemini-3.1-pro",
		"gemini-3-pro":                 "gemini-3-pro",
		"gemini-3-flash":               "gemini-3-flash",
		"claude-sonnet-4-6":            "claude-sonnet-4-6",
		"claude-opus-4-6":              "claude-opus-4-6",
		"gpt-oss-120b":                 "gpt-oss-120b",
	}

	for input, want := range cases {
		input := input
		want := want
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			payload, err := buildRequest(state.AccountAuth{ProjectID: "project-123"}, compat.NormalizedRequest{Model: input})
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if got := payload["model"]; got != want {
				t.Fatalf("model = %#v, want %q", got, want)
			}
		})
	}
}
