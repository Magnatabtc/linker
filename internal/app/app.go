package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"linker/internal/api"
	"linker/internal/catalog"
	"linker/internal/claude"
	"linker/internal/config"
	"linker/internal/logging"
	"linker/internal/onboard"
	"linker/internal/platform"
	"linker/internal/provider"
	"linker/internal/providers/geminiapikey"
	"linker/internal/runtime"
	"linker/internal/service"
	"linker/internal/state"
	"linker/internal/tui"
)

const Version = "0.1.0"

func Run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) (int, error) {
	env := platform.Detect()
	layout := state.NewLayout(env.LinkerDir())
	repo := state.NewRepository(layout)
	if err := repo.Init(); err != nil {
		return 1, err
	}
	defaultCfg := config.Default(env.ClaudeSettingsPath())
	store := config.NewStore(layout.ConfigFile)
	cfg, err := store.Load(defaultCfg)
	if err != nil {
		return 1, err
	}
	providers := provider.NewRegistry()
	catalogService := catalog.New(repo, providers)
	serviceManager := service.NewManager(env, layout)
	supervisor := runtime.NewSupervisor(repo, cfg.Bind, cfg.Port)

	if len(args) == 0 {
		printHelp(stdout)
		return 0, nil
	}

	switch args[0] {
	case "help", "--help", "-h":
		printHelp(stdout)
		return 0, nil
	case "version":
		fmt.Fprintln(stdout, Version)
		return 0, nil
	case "onboard":
		wizard := onboard.New(stdin, stdout, store, repo, providers, catalogService, serviceManager, defaultCfg)
		cfg, err = wizard.Run(ctx)
		if err != nil {
			return 1, err
		}
		fmt.Fprintf(stdout, "\nLinker configured on http://%s:%d\n", cfg.Bind, cfg.Port)
		if err := serviceManager.StartInstalled(); err == nil {
			return 0, nil
		}
		return 0, supervisor.StartBackground(ctx, mustExecutable())
	case "serve":
		return runServer(ctx, repo, store, providers, catalogService, cfg)
	case "start":
		if hasFlag(args[1:], "--fg") {
			return runServer(ctx, repo, store, providers, catalogService, cfg)
		}
		if err := serviceManager.StartInstalled(); err == nil {
			return 0, nil
		}
		return 0, supervisor.StartBackground(ctx, mustExecutable())
	case "stop":
		if err := serviceManager.StopInstalled(); err == nil {
			return 0, nil
		}
		return 0, supervisor.Stop()
	case "restart":
		status, _ := supervisor.Status()
		if status.Running {
			if err := supervisor.Stop(); err != nil {
				return 1, err
			}
			time.Sleep(300 * time.Millisecond)
		}
		if hasFlag(args[1:], "--fg") {
			return runServer(ctx, repo, store, providers, catalogService, cfg)
		}
		if err := serviceManager.StopInstalled(); err == nil {
			time.Sleep(300 * time.Millisecond)
			if startErr := serviceManager.StartInstalled(); startErr == nil {
				return 0, nil
			}
		}
		return 0, supervisor.StartBackground(ctx, mustExecutable())
	case "status":
	        status, err := supervisor.Status()
	        if err != nil {
	                return 1, err
	        }
	        providersSummary := activeProvidersSummary(cfg)
	        if status.Running {
	                fmt.Fprintf(stdout, "running pid=%d port=%d uptime=%s providers=%s service=%s\n", status.PID, cfg.Port, formatUptime(status.StartedAt), providersSummary, serviceManager.Status())
	        } else {
	                fmt.Fprintf(stdout, "stopped port=%d providers=%s service=%s\n", cfg.Port, providersSummary, serviceManager.Status())
	        }
	        return 0, nil
	case "tui":
	        return runDashboard(cfg, providers, serviceManager)
	case "logs":
	        return 0, tailLog(ctx, layout.LogFile, stdout, hasFlag(args[1:], "-f") || hasFlag(args[1:], "--follow"))
	case "config":
	        return runConfigCommand(stdout, store, defaultCfg, args[1:])
	case "account":
	        return runAccountCommand(ctx, stdin, stdout, store, repo, providers, cfg, args[1:])
	case "apikey":
	        return runAPIKeyCommand(ctx, stdin, stdout, store, repo, providers, cfg, args[1:])
	default:
	        printHelp(stderr)
	        return 1, fmt.Errorf("unknown command %q", args[0])
	}
	}

	func runDashboard(cfg config.Config, providers *provider.Registry, services *service.Manager) (int, error) {
	p := tea.NewProgram(tui.NewDashboard(cfg, providers, services), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
	        return 1, err
	}
	return 0, nil
	}

	func runServer(ctx context.Context, repo *state.Repository, store *config.Store, providers *provider.Registry, catalogService *catalog.Service, cfg config.Config) (int, error) {

	logger, err := logging.Open(repo.Layout().LogFile)
	if err != nil {
		return 1, err
	}
	defer logger.Close()
	if _, err := catalogService.Refresh(ctx, cfg); err != nil {
		logger.Println("catalog refresh warning:", err)
	}
	server := api.New(cfg, repo, store, providers, catalogService, logger)
	return 0, server.Serve(ctx)
}

func runConfigCommand(stdout io.Writer, store *config.Store, defaultCfg config.Config, args []string) (int, error) {
	if len(args) < 2 {
		return 1, errors.New("usage: linker config get <key> | linker config set <key> <value>")
	}
	switch args[0] {
	case "get":
		value, err := store.Get(defaultCfg, args[1])
		if err != nil {
			return 1, err
		}
		fmt.Fprintln(stdout, value)
		return 0, nil
	case "set":
		if len(args) < 3 {
			return 1, errors.New("usage: linker config set <key> <value>")
		}
		if _, err := store.Set(defaultCfg, args[1], args[2]); err != nil {
			return 1, err
		}
		fmt.Fprintln(stdout, "ok")
		return 0, nil
	default:
		return 1, fmt.Errorf("unknown config subcommand %q", args[0])
	}
}

func runAccountCommand(ctx context.Context, stdin io.Reader, stdout io.Writer, store *config.Store, repo *state.Repository, providers *provider.Registry, cfg config.Config, args []string) (int, error) {
	if len(args) == 0 {
		return 1, errors.New("usage: linker account list|add|remove|switch")
	}
	switch args[0] {
	case "list":
		for providerID, providerCfg := range cfg.Providers {
			for _, account := range providerCfg.Accounts {
				status := "inactive"
				if account.Active || providerCfg.ActiveAccountID == account.ID {
					status = "active"
				}
				fmt.Fprintf(stdout, "%s %s %s\n", providerID, account.Email, status)
			}
		}
		return 0, nil
	case "add":
		if len(args) < 2 {
			return 1, errors.New("usage: linker account add <provider>")
		}
		info, ok := providers.Get(args[1])
		if !ok {
			return 1, fmt.Errorf("unknown provider %q", args[1])
		}
		if info.AuthKind != "oauth" {
			return 1, fmt.Errorf("provider %q does not use account-based OAuth login", args[1])
		}
		wizard := onboard.New(stdin, stdout, store, repo, providers, catalog.New(repo, providers), service.NewManager(platform.Detect(), repo.Layout()), config.Default(platform.Detect().ClaudeSettingsPath()))
		auth, authPath, err := wizard.CaptureAccountForCLI(ctx, info.ID)
		if err != nil {
			return 1, err
		}
		providerCfg := cfg.Providers[info.ID]
		providerCfg.Type = "oauth"
		providerCfg.Enabled = true
		providerCfg.Accounts = append(providerCfg.Accounts, config.AccountRef{ID: auth.ID, Email: auth.Email, Active: len(providerCfg.Accounts) == 0, AuthFile: authPath})
		if providerCfg.ActiveAccountID == "" {
			providerCfg.ActiveAccountID = auth.ID
		}
		cfg.Providers[info.ID] = providerCfg
		return 0, store.Save(cfg)
	case "remove":
		if len(args) < 3 {
			return 1, errors.New("usage: linker account remove <provider> <email>")
		}
		providerCfg := cfg.Providers[args[1]]
		next := []config.AccountRef{}
		for _, account := range providerCfg.Accounts {
			if !strings.EqualFold(account.Email, args[2]) {
				next = append(next, account)
				continue
			}
			authPath := account.AuthFile
			if !filepath.IsAbs(authPath) {
				authPath = filepath.Join(repo.Layout().AuthDir, filepath.Base(authPath))
			}
			_ = repo.DeleteAuth(authPath)
		}
		providerCfg.Accounts = next
		if providerCfg.ActiveAccountID != "" && len(next) > 0 {
			providerCfg.ActiveAccountID = next[0].ID
			next[0].Active = true
			providerCfg.Accounts = next
		}
		cfg.Providers[args[1]] = providerCfg
		return 0, store.Save(cfg)
	case "switch":
		if len(args) < 3 {
			return 1, errors.New("usage: linker account switch <provider> <email>")
		}
		providerCfg := cfg.Providers[args[1]]
		for i := range providerCfg.Accounts {
			active := strings.EqualFold(providerCfg.Accounts[i].Email, args[2])
			providerCfg.Accounts[i].Active = active
			if active {
				providerCfg.ActiveAccountID = providerCfg.Accounts[i].ID
			}
		}
		cfg.Providers[args[1]] = providerCfg
		return 0, store.Save(cfg)
	default:
		return 1, fmt.Errorf("unknown account subcommand %q", args[0])
	}
}

func tailLog(ctx context.Context, path string, stdout io.Writer, follow bool) error {
	data, err := os.ReadFile(path)
	if err == nil {
		fmt.Fprint(stdout, string(data))
	}
	if !follow {
		return err
	}
	lastSize := int64(len(data))
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			info, statErr := os.Stat(path)
			if statErr != nil || info.Size() <= lastSize {
				continue
			}
			file, openErr := os.Open(path)
			if openErr != nil {
				continue
			}
			if _, seekErr := file.Seek(lastSize, io.SeekStart); seekErr == nil {
				io.Copy(stdout, file)
			}
			lastSize = info.Size()
			file.Close()
		}
	}
}

func printHelp(w io.Writer) {
        fmt.Fprintln(w, "linker commands:")
        fmt.Fprintln(w, "  linker tui")
        fmt.Fprintln(w, "  linker onboard")
        fmt.Fprintln(w, "  linker start [--fg]")

	fmt.Fprintln(w, "  linker stop")
	fmt.Fprintln(w, "  linker restart [--fg]")
	fmt.Fprintln(w, "  linker status")
	fmt.Fprintln(w, "  linker logs [-f]")
	fmt.Fprintln(w, "  linker account list|add|remove|switch")
	fmt.Fprintln(w, "  linker apikey list|add|remove|test|stats gemini")
	fmt.Fprintln(w, "  linker config get|set")
	fmt.Fprintln(w, "  linker version")
}

func activeProvidersSummary(cfg config.Config) string {
	providers := []string{}
	for _, info := range provider.NewRegistry().List() {
		if providerCfg, ok := cfg.Providers[info.ID]; ok && providerCfg.Enabled {
			providers = append(providers, info.ID)
		}
	}
	if len(providers) == 0 {
		return "none"
	}
	return strings.Join(providers, ",")
}

func formatUptime(startedAt time.Time) string {
	if startedAt.IsZero() {
		return "unknown"
	}
	elapsed := time.Since(startedAt).Round(time.Second)
	if elapsed < 0 {
		elapsed = 0
	}
	return elapsed.String()
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func mustExecutable() string {
	value, err := os.Executable()
	if err != nil {
		return "linker"
	}
	return value
}

func ApplyClaudeSettings(path string, cfg config.Config) error {
	raw, err := claude.Merge(path, claude.DesiredEnv(cfg))
	if err != nil {
		return err
	}
	return claude.Save(path, raw)
}

func runAPIKeyCommand(ctx context.Context, stdin io.Reader, stdout io.Writer, store *config.Store, repo *state.Repository, providers *provider.Registry, cfg config.Config, args []string) (int, error) {
	if len(args) < 2 {
		return 1, errors.New("usage: linker apikey list|add|remove|test|stats gemini")
	}
	providerID, err := normalizeAPIKeyProvider(args[1])
	if err != nil {
		return 1, err
	}
	providerCfg := cfg.Providers[providerID]
	auth, err := loadProviderAuth(repo, providerCfg)
	if err != nil && args[0] != "add" {
		return 1, err
	}

	switch args[0] {
	case "list":
		if providerID != "gemini-apikey" {
			return 1, fmt.Errorf("provider %q does not support pooled API keys", providerID)
		}
		for index, key := range auth.Keys {
			status := key.Status
			if status == "" {
				status = "active"
			}
			fmt.Fprintf(stdout, "%d %s %s %s\n", index+1, key.Label, geminiapikey.MaskKey(key.Key), status)
		}
		return 0, nil
	case "add":
		wizard := onboard.New(stdin, stdout, store, repo, providers, catalog.New(repo, providers), service.NewManager(platform.Detect(), repo.Layout()), config.Default(platform.Detect().ClaudeSettingsPath()))
		var existing *state.AccountAuth
		if auth.Provider != "" {
			existing = &auth
		}
		updatedAuth, relPath, info, err := wizard.CaptureProviderForCLI(ctx, providerID, existing)
		if err != nil {
			return 1, err
		}
		providerCfg = config.ProviderConfig{Type: info.AuthKind, Enabled: true, AuthFile: relPath, RotationStrategy: updatedAuth.RotationStrategy}
		cfg.Providers[providerID] = providerCfg
		if err := store.Save(cfg); err != nil {
			return 1, err
		}
		fmt.Fprintln(stdout, "ok")
		return 0, nil
	case "remove":
		if len(args) < 3 {
			return 1, errors.New("usage: linker apikey remove gemini <label|index>")
		}
		auth = removeAPIKey(auth, args[2])
		if len(auth.Keys) == 0 {
			return 1, errors.New("cannot leave Gemini API Key pool empty")
		}
		if _, err := repo.SaveAuth(auth); err != nil {
			return 1, err
		}
		fmt.Fprintln(stdout, "ok")
		return 0, nil
	case "test":
		if providerID != "gemini-apikey" {
			return 1, fmt.Errorf("provider %q does not support API key testing", providerID)
		}
		auth, testErr := geminiapikey.TestPool(ctx, auth)
		if _, err := repo.SaveAuth(auth); err != nil {
			return 1, err
		}
		for _, key := range auth.Keys {
			status := key.Status
			if status == "" {
				status = "active"
			}
			fmt.Fprintf(stdout, "%s %s %s\n", key.Label, geminiapikey.MaskKey(key.Key), status)
		}
		if testErr != nil {
			return 1, testErr
		}
		return 0, nil
	case "stats":
		for _, key := range auth.Keys {
			fmt.Fprintf(stdout, "%s requests=%d rate_limits=%d last_error=%q\n", key.Label, key.Requests, key.RateLimits, key.LastError)
		}
		return 0, nil
	default:
		return 1, fmt.Errorf("unknown apikey subcommand %q", args[0])
	}
}

func normalizeAPIKeyProvider(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gemini", "gemini-apikey":
		return "gemini-apikey", nil
	default:
		return "", fmt.Errorf("unsupported apikey provider %q", value)
	}
}

func loadProviderAuth(repo *state.Repository, providerCfg config.ProviderConfig) (state.AccountAuth, error) {
	if strings.TrimSpace(providerCfg.AuthFile) == "" {
		return state.AccountAuth{}, os.ErrNotExist
	}
	authPath := providerCfg.AuthFile
	if !filepath.IsAbs(authPath) {
		authPath = filepath.Join(repo.Layout().Root, authPath)
	}
	auth, err := repo.LoadAuth(authPath)
	return auth, err
}

func removeAPIKey(auth state.AccountAuth, selector string) state.AccountAuth {
	next := make([]state.APIKeyEntry, 0, len(auth.Keys))
	var index int
	if _, err := fmt.Sscanf(selector, "%d", &index); err == nil && index > 0 {
		for i, key := range auth.Keys {
			if i == index-1 {
				continue
			}
			next = append(next, key)
		}
		auth.Keys = next
		return auth
	}
	for _, key := range auth.Keys {
		if !strings.EqualFold(key.Label, selector) {
			next = append(next, key)
		}
	}
	auth.Keys = next
	return auth
}
