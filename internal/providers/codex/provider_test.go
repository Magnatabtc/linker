package codex

import (
	"reflect"
	"testing"
)

func TestInfoAdvertisesCurrentCodexModels(t *testing.T) {
	t.Parallel()

	info := New().Info()
	want := []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex"}

	if !reflect.DeepEqual(info.Models, want) {
		t.Fatalf("models = %#v, want %#v", info.Models, want)
	}
	if info.ID != "codex" {
		t.Fatalf("id = %q, want %q", info.ID, "codex")
	}
	if info.DisplayName != "Codex" {
		t.Fatalf("display name = %q, want %q", info.DisplayName, "Codex")
	}
	if info.AuthKind != "oauth" {
		t.Fatalf("auth kind = %q, want %q", info.AuthKind, "oauth")
	}
}
