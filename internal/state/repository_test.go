package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRepositoryAuthAndRegistryRoundTrip(t *testing.T) {
	t.Parallel()

	layout := NewLayout(t.TempDir())
	repo := NewRepository(layout)
	if err := repo.Init(); err != nil {
		t.Fatalf("init repo: %v", err)
	}

	auth := AccountAuth{
		ID:           "account_1",
		Provider:     "codex",
		Email:        "user@example.com",
		AccessToken:  "token",
		BaseURL:      "http://localhost:9000",
		UpstreamType: "openai",
		ExpiresAt:    time.Now().UTC(),
	}
	path, err := repo.SaveAuth(auth)
	if err != nil {
		t.Fatalf("save auth: %v", err)
	}
	if filepath.Dir(path) != layout.AuthDir {
		t.Fatalf("unexpected auth path: %s", path)
	}

	gotAuth, err := repo.LoadAuth(path)
	if err != nil {
		t.Fatalf("load auth: %v", err)
	}
	if gotAuth.Email != auth.Email || gotAuth.AccessToken != auth.AccessToken {
		t.Fatalf("unexpected auth: %#v", gotAuth)
	}

	reg := ModelRegistry{
		Entries: map[string][]DiscoveredModel{
			"codex:account_1": {{
				Provider:  "codex",
				AccountID: "account_1",
				Name:      "gpt-5-codex",
				Label:     "gpt-5-codex [user@example.com]",
				UpdatedAt: time.Now().UTC(),
			}},
		},
	}
	if err := repo.SaveRegistry(reg); err != nil {
		t.Fatalf("save registry: %v", err)
	}
	gotReg, err := repo.LoadRegistry()
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(gotReg.Entries["codex:account_1"]) != 1 {
		t.Fatalf("unexpected registry: %#v", gotReg)
	}
}

func TestRepositorySaveLoadAPIKeyPool(t *testing.T) {
	t.Parallel()

	layout := NewLayout(t.TempDir())
	repo := NewRepository(layout)
	if err := repo.Init(); err != nil {
		t.Fatalf("init repo: %v", err)
	}

	auth := AccountAuth{
		ID:               "gemini-apikey",
		Provider:         "gemini-apikey",
		Email:            "gemini-apikey-pool",
		AuthType:         "apikey_pool",
		BaseURL:          "https://generativelanguage.googleapis.com/v1beta/openai",
		UpstreamType:     "openai",
		RotationStrategy: "round-robin",
		Keys: []APIKeyEntry{{
			Key:      "AIzaSyExample",
			Label:    "key-1",
			AddedAt:  time.Now().UTC(),
			Status:   "active",
			Requests: 4,
		}},
	}
	path, err := repo.SaveAuth(auth)
	if err != nil {
		t.Fatalf("save auth: %v", err)
	}
	if filepath.Base(path) != "gemini-apikey.json" {
		t.Fatalf("unexpected auth filename: %s", path)
	}

	got, err := repo.LoadAuth(path)
	if err != nil {
		t.Fatalf("load auth: %v", err)
	}
	if got.AuthType != "apikey_pool" || len(got.Keys) != 1 || got.Keys[0].Label != "key-1" {
		t.Fatalf("unexpected auth payload: %#v", got)
	}
}

func TestRepositorySaveAuthUsesEmailInOAuthFilename(t *testing.T) {
	t.Parallel()

	layout := NewLayout(t.TempDir())
	repo := NewRepository(layout)
	if err := repo.Init(); err != nil {
		t.Fatalf("init repo: %v", err)
	}

	path, err := repo.SaveAuth(AccountAuth{
		ID:           "gemini-cli_user_example_com",
		Provider:     "gemini-cli",
		Email:        "user@example.com",
		AuthType:     "oauth",
		AccessToken:  "token",
		BaseURL:      "https://example.test",
		UpstreamType: "openai",
	})
	if err != nil {
		t.Fatalf("save auth: %v", err)
	}
	if filepath.Base(path) != "gemini-cli_user@example.com.json" {
		t.Fatalf("unexpected oauth auth filename: %s", path)
	}
}
