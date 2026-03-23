package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Layout struct {
	Root          string
	ConfigFile    string
	AuthDir       string
	ModelRegistry string
	LogDir        string
	LogFile       string
	PIDFile       string
	ServiceDir    string
	InstallDir    string
}

func NewLayout(root string) Layout {
	return Layout{
		Root:          root,
		ConfigFile:    filepath.Join(root, "config.json"),
		AuthDir:       filepath.Join(root, "auth"),
		ModelRegistry: filepath.Join(root, "model-registry.json"),
		LogDir:        filepath.Join(root, "logs"),
		LogFile:       filepath.Join(root, "logs", "linker.log"),
		PIDFile:       filepath.Join(root, "linker.pid"),
		ServiceDir:    filepath.Join(root, "service"),
		InstallDir:    filepath.Join(root, "install"),
	}
}

type Repository struct {
	layout Layout
}

type AccountAuth struct {
	ID               string            `json:"id"`
	Provider         string            `json:"provider"`
	Email            string            `json:"email"`
	AuthType         string            `json:"auth_type,omitempty"`
	AccessToken      string            `json:"access_token"`
	RefreshToken     string            `json:"refresh_token,omitempty"`
	ExpiresAt        time.Time         `json:"expires_at,omitempty"`
	APIKey           string            `json:"api_key,omitempty"`
	BaseURL          string            `json:"base_url"`
	UpstreamType     string            `json:"upstream_type"`
	RefreshURL       string            `json:"refresh_url,omitempty"`
	ClientID         string            `json:"client_id,omitempty"`
	ClientSecret     string            `json:"client_secret,omitempty"`
	TokenURL         string            `json:"token_url,omitempty"`
	AuthorizationURL string            `json:"authorization_url,omitempty"`
	DeviceURL        string            `json:"device_url,omitempty"`
	ProjectID        string            `json:"project_id,omitempty"`
	Keys             []APIKeyEntry     `json:"keys,omitempty"`
	RotationIndex    int               `json:"rotation_index,omitempty"`
	RotationStrategy string            `json:"rotation_strategy,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type APIKeyEntry struct {
	Key             string     `json:"key"`
	Label           string     `json:"label"`
	AddedAt         time.Time  `json:"added_at"`
	Status          string     `json:"status,omitempty"`
	LastRateLimited *time.Time `json:"last_rate_limited,omitempty"`
	Requests        int        `json:"requests,omitempty"`
	RateLimits      int        `json:"rate_limits,omitempty"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
}

type ModelRegistry struct {
	UpdatedAt time.Time                    `json:"updated_at"`
	Entries   map[string][]DiscoveredModel `json:"entries"`
}

type DiscoveredModel struct {
	Provider  string    `json:"provider"`
	AccountID string    `json:"account_id"`
	Name      string    `json:"name"`
	Label     string    `json:"label"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewRepository(layout Layout) *Repository {
	return &Repository{layout: layout}
}

func (r *Repository) Layout() Layout {
	return r.layout
}

func (r *Repository) Init() error {
	dirs := map[string]os.FileMode{
		r.layout.Root:       0o700,
		r.layout.AuthDir:    0o700,
		r.layout.LogDir:     0o700,
		r.layout.ServiceDir: 0o700,
		r.layout.InstallDir: 0o700,
	}
	for dir, mode := range dirs {
		if err := os.MkdirAll(dir, mode); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) SavePID(pid int) error {
	if err := r.Init(); err != nil {
		return err
	}
	return os.WriteFile(r.layout.PIDFile, []byte(fmt.Sprintf("%d\n", pid)), 0o600)
}

func (r *Repository) PIDInfo() (int, time.Time, error) {
	pid, err := r.LoadPID()
	if err != nil {
		return 0, time.Time{}, err
	}
	info, err := os.Stat(r.layout.PIDFile)
	if err != nil {
		return 0, time.Time{}, err
	}
	return pid, info.ModTime().UTC(), nil
}

func (r *Repository) LoadPID() (int, error) {
	data, err := os.ReadFile(r.layout.PIDFile)
	if err != nil {
		return 0, err
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		return 0, fmt.Errorf("decode pid: %w", err)
	}
	return pid, nil
}

func (r *Repository) RemovePID() error {
	err := os.Remove(r.layout.PIDFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (r *Repository) authPath(provider, accountID string) string {
	sanitizedProvider := sanitize(provider)
	sanitizedID := sanitize(accountID)
	if sanitizedID == "" {
		sanitizedID = sanitizedProvider
	}
	if sanitizedID == sanitizedProvider || strings.HasPrefix(sanitizedID, sanitizedProvider+"_") {
		return filepath.Join(r.layout.AuthDir, sanitizedID+".json")
	}
	return filepath.Join(r.layout.AuthDir, sanitizedProvider+"_"+sanitizedID+".json")
}

func (r *Repository) SaveAuth(auth AccountAuth) (string, error) {
	if err := r.Init(); err != nil {
		return "", err
	}
	if auth.ID == "" {
		return "", errors.New("account auth requires id")
	}
	path := r.authPathForAuth(auth)
	if err := writeJSONAtomic(path, auth); err != nil {
		return "", err
	}
	return path, nil
}

func (r *Repository) authPathForAuth(auth AccountAuth) string {
	provider := sanitize(auth.Provider)
	if auth.AuthType == "oauth" && strings.TrimSpace(auth.Email) != "" {
		filename := provider + "_" + sanitizeFilename(strings.ToLower(strings.TrimSpace(auth.Email))) + ".json"
		return filepath.Join(r.layout.AuthDir, filename)
	}
	return r.authPath(auth.Provider, auth.ID)
}

func (r *Repository) LoadAuth(path string) (AccountAuth, error) {
	var auth AccountAuth
	err := readJSON(r.ResolveAuthPath(path), &auth)
	if auth.RotationStrategy == "" && len(auth.Keys) > 0 {
		auth.RotationStrategy = "round-robin"
	}
	return auth, err
}

func (r *Repository) DeleteAuth(path string) error {
	err := os.Remove(r.ResolveAuthPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (r *Repository) ResolveAuthPath(path string) string {
	value := strings.TrimSpace(path)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		if isWithinDir(r.layout.AuthDir, value) {
			return value
		}
		return filepath.Join(r.layout.AuthDir, filepath.Base(value))
	}
	candidate := filepath.Join(r.layout.Root, value)
	if isWithinDir(r.layout.AuthDir, candidate) {
		return candidate
	}
	return filepath.Join(r.layout.AuthDir, filepath.Base(value))
}

func (r *Repository) Relative(path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(r.layout.Root, path)
	if err != nil {
		return path
	}
	return rel
}

func (r *Repository) SaveRegistry(reg ModelRegistry) error {
	if err := r.Init(); err != nil {
		return err
	}
	if reg.Entries == nil {
		reg.Entries = map[string][]DiscoveredModel{}
	}
	reg.UpdatedAt = time.Now().UTC()
	return writeJSONAtomic(r.layout.ModelRegistry, reg)
}

func (r *Repository) LoadRegistry() (ModelRegistry, error) {
	reg := ModelRegistry{Entries: map[string][]DiscoveredModel{}}
	err := readJSON(r.layout.ModelRegistry, &reg)
	if errors.Is(err, os.ErrNotExist) {
		return reg, nil
	}
	return reg, err
}

func (r *Repository) ListAuthFiles() ([]string, error) {
	entries, err := os.ReadDir(r.layout.AuthDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		paths = append(paths, filepath.Join(r.layout.AuthDir, entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
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

func sanitize(value string) string {
	replacer := strings.NewReplacer("@", "_", "/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(strings.ToLower(value))
}

func sanitizeFilename(value string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return replacer.Replace(strings.TrimSpace(value))
}

func isWithinDir(dir string, candidate string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, absCandidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
