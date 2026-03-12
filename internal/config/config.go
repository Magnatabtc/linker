package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const SchemaVersion = 1

type Config struct {
	Version              int                       `json:"version"`
	Port                 int                       `json:"port"`
	Bind                 string                    `json:"bind"`
	LogLevel             string                    `json:"log_level"`
	Providers            map[string]ProviderConfig `json:"providers"`
	ModelMapping         ModelMapping              `json:"model_mapping"`
	ClaudeCode           ClaudeCodeConfig          `json:"claude_code"`
	MultiAccountStrategy string                    `json:"multi_account_strategy"`
}

type ProviderConfig struct {
	Type             string       `json:"type,omitempty"`
	Enabled          bool         `json:"enabled"`
	Accounts         []AccountRef `json:"accounts,omitempty"`
	ActiveAccountID  string       `json:"active_account_id,omitempty"`
	AuthFile         string       `json:"auth_file,omitempty"`
	RotationStrategy string       `json:"rotation_strategy,omitempty"`
}

type AccountRef struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Active   bool   `json:"active"`
	AuthFile string `json:"auth_file"`
}

type ModelMapping struct {
	Default ModelTarget `json:"default"`
	Opus    ModelTarget `json:"opus"`
	Sonnet  ModelTarget `json:"sonnet"`
	Haiku   ModelTarget `json:"haiku"`
}

type ModelTarget struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type ClaudeCodeConfig struct {
	AutoConfigure bool   `json:"auto_configure"`
	SettingsPath  string `json:"settings_path"`
}

type Store struct {
	path string
	mu   sync.RWMutex
}

func Default(settingsPath string) Config {
	return Config{
		Version:              SchemaVersion,
		Port:                 6767,
		Bind:                 "127.0.0.1",
		LogLevel:             "info",
		Providers:            map[string]ProviderConfig{},
		ClaudeCode:           ClaudeCodeConfig{AutoConfigure: true, SettingsPath: settingsPath},
		MultiAccountStrategy: "sticky-fallback",
	}
}

func (p ProviderConfig) UsesAccounts() bool {
	return p.Type == "oauth" || (p.Type == "" && len(p.Accounts) > 0)
}

func (p ProviderConfig) UsesProviderAuth() bool {
	return !p.UsesAccounts()
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Load(defaultConfig Config) (Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultConfig, nil
	}
	if err != nil {
		return Config{}, err
	}

	cfg := defaultConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Version == 0 {
		cfg.Version = SchemaVersion
	}
	if cfg.Port == 0 {
		cfg.Port = 6767
	}
	if cfg.Bind == "" {
		cfg.Bind = "127.0.0.1"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}
	cfg.Providers = normalizeProviders(cfg.Providers)
	if cfg.MultiAccountStrategy == "" {
		cfg.MultiAccountStrategy = "sticky-fallback"
	}
	return cfg, nil
}

func (s *Store) Save(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg.Version = SchemaVersion
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}
	cfg.Providers = normalizeProviders(cfg.Providers)
	if cfg.Bind == "" {
		cfg.Bind = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 6767
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.MultiAccountStrategy == "" {
		cfg.MultiAccountStrategy = "sticky-fallback"
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Get(defaultConfig Config, key string) (string, error) {
	cfg, err := s.Load(defaultConfig)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(key) {
	case "port":
		return fmt.Sprintf("%d", cfg.Port), nil
	case "bind":
		return cfg.Bind, nil
	case "log_level":
		return cfg.LogLevel, nil
	case "claude_code.settings_path":
		return cfg.ClaudeCode.SettingsPath, nil
	default:
		return "", fmt.Errorf("unsupported key %q", key)
	}
}

func (s *Store) Set(defaultConfig Config, key string, value string) (Config, error) {
	cfg, err := s.Load(defaultConfig)
	if err != nil {
		return Config{}, err
	}
	switch strings.ToLower(key) {
	case "port":
		var port int
		if _, err := fmt.Sscanf(value, "%d", &port); err != nil || port <= 0 {
			return Config{}, fmt.Errorf("invalid port %q", value)
		}
		cfg.Port = port
	case "bind":
		cfg.Bind = value
	case "log_level":
		cfg.LogLevel = value
	case "claude_code.settings_path":
		cfg.ClaudeCode.SettingsPath = value
	default:
		return Config{}, fmt.Errorf("unsupported key %q", key)
	}
	return cfg, s.Save(cfg)
}

func normalizeProviders(providers map[string]ProviderConfig) map[string]ProviderConfig {
	if providers == nil {
		return map[string]ProviderConfig{}
	}
	for id, providerCfg := range providers {
		if providerCfg.Type == "" {
			providerCfg.Type = defaultProviderType(id)
		}
		if providerCfg.Type == "apikey-pool" && providerCfg.RotationStrategy == "" {
			providerCfg.RotationStrategy = "round-robin"
		}
		providers[id] = providerCfg
	}
	return providers
}

func defaultProviderType(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "gemini-apikey":
		return "apikey-pool"
	case "codingplan":
		return "apikey"
	default:
		return "oauth"
	}
}
