package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"linker/internal/config"
)

type Settings struct {
	Env map[string]string `json:"env"`
	Raw map[string]any    `json:"-"`
}

func DesiredEnv(cfg config.Config) map[string]string {
	baseHost := strings.TrimSpace(cfg.Bind)
	switch baseHost {
	case "", "127.0.0.1", "::1":
		baseHost = "localhost"
	}
	return map[string]string{
		"ANTHROPIC_BASE_URL":                       fmt.Sprintf("http://%s:%d", baseHost, cfg.Port),
		"ANTHROPIC_AUTH_TOKEN":                     "linker-local",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":             cfg.ModelMapping.Opus.Model,
		"ANTHROPIC_DEFAULT_SONNET_MODEL":           cfg.ModelMapping.Sonnet.Model,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":            cfg.ModelMapping.Haiku.Model,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	}
}

func Load(path string) (map[string]any, error) {
	raw := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return raw, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func Merge(path string, env map[string]string) (map[string]any, error) {
	raw, err := Load(path)
	if err != nil {
		return nil, err
	}
	currentEnv, _ := raw["env"].(map[string]any)
	if currentEnv == nil {
		currentEnv = map[string]any{}
	}
	for key, value := range env {
		currentEnv[key] = value
	}
	raw["env"] = currentEnv
	return raw, nil
}

func Preview(path string, env map[string]string) ([]string, map[string]any, error) {
	raw, err := Load(path)
	if err != nil {
		return nil, nil, err
	}
	currentEnv, _ := raw["env"].(map[string]any)
	if currentEnv == nil {
		currentEnv = map[string]any{}
	}
	lines := make([]string, 0, len(env))
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		next := env[key]
		current := fmt.Sprintf("%v", currentEnv[key])
		if current == "<nil>" {
			current = ""
		}
		if current == next {
			lines = append(lines, fmt.Sprintf("  = %s=%s", key, next))
			continue
		}
		if current == "" {
			lines = append(lines, fmt.Sprintf("  + %s=%s", key, next))
			continue
		}
		lines = append(lines, fmt.Sprintf("  - %s=%s", key, current))
		lines = append(lines, fmt.Sprintf("  + %s=%s", key, next))
	}
	merged := map[string]any{}
	for key, value := range raw {
		merged[key] = value
	}
	newEnv := map[string]any{}
	for key, value := range currentEnv {
		newEnv[key] = value
	}
	for key, value := range env {
		newEnv[key] = value
	}
	merged["env"] = newEnv
	return lines, merged, nil
}

func Save(path string, raw map[string]any) error {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
