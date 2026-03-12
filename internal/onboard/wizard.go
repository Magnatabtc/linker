package onboard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"linker/internal/catalog"
	"linker/internal/claude"
	"linker/internal/config"
	"linker/internal/platform"
	"linker/internal/provider"
	"linker/internal/providerkit"
	"linker/internal/service"
	"linker/internal/state"
)

const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
)

type Wizard struct {
	in         *bufio.Reader
	out        io.Writer
	store      *config.Store
	repo       *state.Repository
	providers  *provider.Registry
	catalog    *catalog.Service
	services   *service.Manager
	defaultCfg config.Config
	env        platform.Environment
}

func New(in io.Reader, out io.Writer, store *config.Store, repo *state.Repository, providers *provider.Registry, catalog *catalog.Service, services *service.Manager, defaultCfg config.Config) *Wizard {
	return &Wizard{
		in:         bufio.NewReader(in),
		out:        out,
		store:      store,
		repo:       repo,
		providers:  providers,
		catalog:    catalog,
		services:   services,
		defaultCfg: defaultCfg,
		env:        platform.Detect(),
	}
}

func (w *Wizard) Run(ctx context.Context) (config.Config, error) {
	cfg, err := w.store.Load(w.defaultCfg)
	if err != nil {
		return config.Config{}, err
	}

	fmt.Fprintln(w.out, w.paint(colorCyan, "Welcome to Linker!"))
	fmt.Fprintln(w.out, "  Your local AI bridge for Claude Code.")
	mode := w.ask("Choose setup mode: [1] QuickStart [2] Advanced", "1")
	advanced := strings.TrimSpace(mode) == "2"

	selectedProviders, err := w.providerSelectionFlow(cfg)
	if err != nil {
		return config.Config{}, err
	}

	nextProviders, err := w.configureSelectedProviders(ctx, cfg, selectedProviders)
	if err != nil {
		return config.Config{}, err
	}
	cfg.Providers = nextProviders

	registry, _ := w.catalog.Refresh(ctx, cfg)
	w.configureMappings(&cfg, registry)
	if advanced {
		cfg.Port = w.askInt("Daemon port", cfg.Port)
	}
	if cfg.ClaudeCode.SettingsPath == "" {
		cfg.ClaudeCode = w.defaultCfg.ClaudeCode
	}

	w.printSummary(cfg)
	if strings.EqualFold(strings.TrimSpace(w.ask("Continue with this configuration? [Y/n]", "Y")), "n") {
		return config.Config{}, fmt.Errorf("onboarding cancelled")
	}

	env := claude.DesiredEnv(cfg)
	settingsPath := cfg.ClaudeCode.SettingsPath
	diff, preview, err := claude.Preview(settingsPath, env)
	if err != nil {
		return config.Config{}, err
	}
	fmt.Fprintf(w.out, "\n%s\n", w.paint(colorCyan, "The following changes will be applied to "+settingsPath+":"))
	for _, line := range diff {
		fmt.Fprintln(w.out, line)
	}
	if strings.ToLower(w.ask("Apply to "+settingsPath+"? [Y/n]", "Y")) != "n" {
		if err := claude.Save(settingsPath, preview); err != nil {
			return config.Config{}, err
		}
		fmt.Fprintln(w.out, w.paint(colorGreen, "Claude Code settings updated."))
	}

	if err := w.store.Save(cfg); err != nil {
		return config.Config{}, err
	}
	fmt.Fprintln(w.out, w.paint(colorGreen, "Linker configuration saved."))

	executable, _ := os.Executable()
	if executable != "" {
		if path, err := w.services.Install(executable); err == nil {
			fmt.Fprintln(w.out, w.paint(colorGreen, w.services.InstallHint(path)))
		}
	}
	return cfg, nil
}

func (w *Wizard) CaptureAccountForCLI(ctx context.Context, providerID string) (state.AccountAuth, string, error) {
	auth, authPath, _, err := w.captureAccount(ctx, providerID, nil)
	return auth, authPath, err
}

func (w *Wizard) CaptureProviderForCLI(ctx context.Context, providerID string, existing *state.AccountAuth) (state.AccountAuth, string, provider.Info, error) {
	return w.captureAccount(ctx, providerID, existing)
}

func (w *Wizard) providerSelectionFlow(cfg config.Config) ([]string, error) {
	if len(cfg.Providers) == 0 {
		return w.selectProviders(cfg)
	}

	fmt.Fprintln(w.out, "\n"+w.paint(colorYellow, "Existing configuration detected."))
	for _, line := range w.providerSummaryLines(cfg) {
		fmt.Fprintln(w.out, line)
	}
	choice := strings.ToUpper(strings.TrimSpace(w.ask("Options: [K]eep providers [A]dd provider [R]emove provider [C]hange providers [S]tart fresh", "K")))
	switch choice {
	case "K":
		selected := currentProviderIDs(cfg)
		if len(selected) == 0 {
			return w.selectProviders(cfg)
		}
		return selected, nil
	case "A":
		return w.addProviders(cfg)
	case "R":
		return w.removeProviders(cfg)
	case "S":
		return w.selectProviders(config.Default(cfg.ClaudeCode.SettingsPath))
	default:
		return w.selectProviders(cfg)
	}
}

func (w *Wizard) addProviders(cfg config.Config) ([]string, error) {
	current := currentProviderIDs(cfg)
	available := []provider.Info{}
	for _, info := range w.providers.List() {
		if _, ok := cfg.Providers[info.ID]; ok && cfg.Providers[info.ID].Enabled {
			continue
		}
		available = append(available, info)
	}
	if len(available) == 0 {
		return current, nil
	}
	fmt.Fprintln(w.out, "\nAdd provider(s):")
	for i, info := range available {
		fmt.Fprintf(w.out, "  [%d] %s\n", i+1, info.DisplayName)
	}
	raw := w.ask("Select provider numbers to add", "1")
	for _, chunk := range strings.Split(raw, ",") {
		index := parseChoice(chunk)
		if index <= 0 || index > len(available) {
			continue
		}
		current = appendIfMissing(current, available[index-1].ID)
	}
	sort.Strings(current)
	return current, nil
}

func (w *Wizard) removeProviders(cfg config.Config) ([]string, error) {
	current := currentProviderIDs(cfg)
	if len(current) == 0 {
		return w.selectProviders(cfg)
	}
	fmt.Fprintln(w.out, "\nRemove provider(s):")
	for i, providerID := range current {
		label := providerID
		if info, ok := w.providers.Get(providerID); ok {
			label = info.DisplayName
		}
		fmt.Fprintf(w.out, "  [%d] %s\n", i+1, label)
	}
	raw := w.ask("Select provider numbers to remove", "")
	remove := map[string]bool{}
	for _, chunk := range strings.Split(raw, ",") {
		index := parseChoice(chunk)
		if index <= 0 || index > len(current) {
			continue
		}
		remove[current[index-1]] = true
	}
	selected := []string{}
	for _, providerID := range current {
		if !remove[providerID] {
			selected = append(selected, providerID)
		}
	}
	if len(selected) == 0 {
		return w.selectProviders(cfg)
	}
	return selected, nil
}

func (w *Wizard) selectProviders(cfg config.Config) ([]string, error) {
	fmt.Fprintln(w.out, "\nChoose your AI provider(s):")
	infos := w.providers.List()
	for i, info := range infos {
		fmt.Fprintf(w.out, "  [%d] %s\n", i+1, info.DisplayName)
	}
	defaultSelection := currentProviderSelection(infos, cfg)
	raw := w.ask("Select one or more providers (e.g. 1,3)", defaultSelection)
	chunks := strings.Split(raw, ",")
	selected := []string{}
	for _, chunk := range chunks {
		var index int
		fmt.Sscanf(strings.TrimSpace(chunk), "%d", &index)
		if index <= 0 || index > len(infos) {
			continue
		}
		selected = appendIfMissing(selected, infos[index-1].ID)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("at least one provider must be selected")
	}
	return selected, nil
}

func (w *Wizard) captureAccount(ctx context.Context, providerID string, existing *state.AccountAuth) (state.AccountAuth, string, provider.Info, error) {
	info, _ := w.providers.Get(providerID)
	fmt.Fprintf(w.out, "\n%s\n", w.paint(colorCyan, "Authenticating "+info.DisplayName))
	auth, err := w.providers.Authenticate(ctx, providerID, providerkit.Interactive{
		Env:    w.env,
		Prompt: w.ask,
		Printf: func(format string, args ...any) { fmt.Fprintf(w.out, format, args...) },
		Println: func(args ...any) {
			fmt.Fprintln(w.out, args...)
		},
	}, existing)
	if err != nil {
		return state.AccountAuth{}, "", provider.Info{}, err
	}
	authPath, err := w.repo.SaveAuth(auth)
	if err != nil {
		return state.AccountAuth{}, "", provider.Info{}, err
	}
	fmt.Fprintf(w.out, "%s\n", w.paint(colorGreen, "Authentication completed for "+info.DisplayName))
	return auth, w.repo.Relative(authPath), info, nil
}

func (w *Wizard) configureSelectedProviders(ctx context.Context, cfg config.Config, selectedProviders []string) (map[string]config.ProviderConfig, error) {
	nextProviders := map[string]config.ProviderConfig{}
	for _, providerID := range selectedProviders {
		info, ok := w.providers.Get(providerID)
		if !ok {
			return nil, fmt.Errorf("unknown provider %q", providerID)
		}
		existingProvider := cfg.Providers[providerID]
		var providerCfg config.ProviderConfig
		var err error
		if info.AuthKind == "oauth" {
			providerCfg, err = w.configureOAuthProvider(ctx, providerID, existingProvider)
		} else {
			providerCfg, err = w.configureProviderAuth(ctx, providerID, existingProvider)
		}
		if err != nil {
			return nil, err
		}
		nextProviders[providerID] = providerCfg
	}
	return nextProviders, nil
}

func (w *Wizard) configureProviderAuth(ctx context.Context, providerID string, existingProvider config.ProviderConfig) (config.ProviderConfig, error) {
	existingAuth := w.existingProviderAuth(existingProvider)
	auth, authPath, info, err := w.captureAccount(ctx, providerID, existingAuth)
	if err != nil {
		return config.ProviderConfig{}, err
	}
	providerCfg := config.ProviderConfig{
		Type:     info.AuthKind,
		Enabled:  true,
		AuthFile: authPath,
	}
	if info.AuthKind == "apikey-pool" {
		providerCfg.RotationStrategy = auth.RotationStrategy
	}
	return providerCfg, nil
}

func (w *Wizard) configureOAuthProvider(ctx context.Context, providerID string, existingProvider config.ProviderConfig) (config.ProviderConfig, error) {
	providerCfg := existingProvider
	providerCfg.Type = "oauth"
	providerCfg.Enabled = true
	providerCfg.Accounts = cloneAccounts(providerCfg.Accounts)
	setActiveAccount(&providerCfg, providerCfg.ActiveAccountID)
	if len(providerCfg.Accounts) == 0 {
		auth, authPath, _, err := w.captureAccount(ctx, providerID, nil)
		if err != nil {
			return config.ProviderConfig{}, err
		}
		providerCfg.Accounts = []config.AccountRef{{ID: auth.ID, Email: auth.Email, Active: true, AuthFile: authPath}}
		providerCfg.ActiveAccountID = auth.ID
		return providerCfg, nil
	}

	for {
		fmt.Fprintf(w.out, "\n%s\n", w.paint(colorYellow, "Existing accounts for "+providerID))
		for idx, account := range providerCfg.Accounts {
			status := "fallback"
			if account.Active || providerCfg.ActiveAccountID == account.ID {
				status = "active"
			}
			fmt.Fprintf(w.out, "  [%d] %s (%s)\n", idx+1, account.Email, status)
		}
		choice := strings.ToUpper(strings.TrimSpace(w.ask("Options: [K]eep [S]witch active [A]dd account [R]emove account [C]Re-authenticate active", "K")))
		switch choice {
		case "K":
			setActiveAccount(&providerCfg, providerCfg.ActiveAccountID)
			return providerCfg, nil
		case "S":
			index := w.chooseAccountIndex("Choose active account", providerCfg.Accounts, providerCfg.ActiveAccountID)
			setActiveAccount(&providerCfg, providerCfg.Accounts[index].ID)
		case "A":
			auth, authPath, _, err := w.captureAccount(ctx, providerID, nil)
			if err != nil {
				return config.ProviderConfig{}, err
			}
			providerCfg = upsertAccount(providerCfg, config.AccountRef{ID: auth.ID, Email: auth.Email, Active: false, AuthFile: authPath})
			if strings.EqualFold(w.ask("Set this account as active? [y/N]", "N"), "y") {
				setActiveAccount(&providerCfg, auth.ID)
			}
		case "R":
			index := w.chooseAccountIndex("Choose account to remove", providerCfg.Accounts, providerCfg.ActiveAccountID)
			authPath := ResolveAuthPath(w.repo.Layout().Root, providerCfg.Accounts[index].AuthFile)
			_ = w.repo.DeleteAuth(authPath)
			providerCfg = removeAccount(providerCfg, providerCfg.Accounts[index].ID)
			if len(providerCfg.Accounts) == 0 {
				auth, authPath, _, err := w.captureAccount(ctx, providerID, nil)
				if err != nil {
					return config.ProviderConfig{}, err
				}
				providerCfg.Accounts = []config.AccountRef{{ID: auth.ID, Email: auth.Email, Active: true, AuthFile: authPath}}
				providerCfg.ActiveAccountID = auth.ID
			}
		case "C":
			active := activeAccount(providerCfg)
			existingAuth := w.loadAccountAuth(active)
			auth, authPath, _, err := w.captureAccount(ctx, providerID, existingAuth)
			if err != nil {
				return config.ProviderConfig{}, err
			}
			providerCfg = upsertAccount(providerCfg, config.AccountRef{ID: auth.ID, Email: auth.Email, Active: true, AuthFile: authPath})
			setActiveAccount(&providerCfg, auth.ID)
		default:
			setActiveAccount(&providerCfg, providerCfg.ActiveAccountID)
			return providerCfg, nil
		}
	}
}

func (w *Wizard) configureMappings(cfg *config.Config, registry state.ModelRegistry) {
	modelsByProvider := map[string][]string{}
	for providerID, providerCfg := range cfg.Providers {
		for _, builtin := range w.providers.BuiltinModels(providerID) {
			modelsByProvider[providerID] = appendIfMissing(modelsByProvider[providerID], builtin.Name)
		}
		if providerCfg.UsesProviderAuth() {
			for _, discovered := range registry.Entries[providerID] {
				modelsByProvider[providerID] = appendIfMissing(modelsByProvider[providerID], discovered.Name)
			}
			continue
		}
		for _, account := range providerCfg.Accounts {
			key := providerID + ":" + account.ID
			for _, discovered := range registry.Entries[key] {
				modelsByProvider[providerID] = appendIfMissing(modelsByProvider[providerID], discovered.Name)
			}
		}
	}
	for providerID := range modelsByProvider {
		sort.Strings(modelsByProvider[providerID])
	}
	providerOrder := []string{}
	for providerID := range cfg.Providers {
		providerOrder = append(providerOrder, providerID)
	}
	sort.Strings(providerOrder)

	fmt.Fprintln(w.out, "\nConfigure model mapping for Claude Code")
	cfg.ModelMapping.Default = w.chooseModelTarget("Default model", cfg.ModelMapping.Default, providerOrder, modelsByProvider)
	cfg.ModelMapping.Opus = w.chooseModelTarget("Opus slot", cfg.ModelMapping.Opus, providerOrder, modelsByProvider)
	cfg.ModelMapping.Sonnet = w.chooseModelTarget("Sonnet slot", cfg.ModelMapping.Sonnet, providerOrder, modelsByProvider)
	cfg.ModelMapping.Haiku = w.chooseModelTarget("Haiku slot", cfg.ModelMapping.Haiku, providerOrder, modelsByProvider)
}

func (w *Wizard) ask(prompt string, fallback string) string {
	if fallback != "" {
		fmt.Fprintf(w.out, "%s [%s]: ", prompt, fallback)
	} else {
		fmt.Fprintf(w.out, "%s: ", prompt)
	}
	line, _ := w.in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return fallback
	}
	return line
}

func (w *Wizard) askInt(prompt string, fallback int) int {
	value := w.ask(prompt, fmt.Sprintf("%d", fallback))
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func ResolveAuthPath(root, value string) string {
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(root, value)
}

func (w *Wizard) chooseModelTarget(prompt string, current config.ModelTarget, providerOrder []string, modelsByProvider map[string][]string) config.ModelTarget {
	defaultProviderIndex := "1"
	for index, providerID := range providerOrder {
		if providerID == current.Provider {
			defaultProviderIndex = fmt.Sprintf("%d", index+1)
			break
		}
	}
	fmt.Fprintf(w.out, "\n%s\n", prompt)
	for index, providerID := range providerOrder {
		label := providerID
		if info, ok := w.providers.Get(providerID); ok {
			label = info.DisplayName
		}
		fmt.Fprintf(w.out, "  [%d] %s\n", index+1, label)
	}
	providerChoice := w.ask("Choose provider", defaultProviderIndex)
	selectedProvider := providerOrder[0]
	if parsed := parseChoice(providerChoice); parsed > 0 && parsed <= len(providerOrder) {
		selectedProvider = providerOrder[parsed-1]
	}
	models := modelsByProvider[selectedProvider]
	if len(models) == 0 {
		models = []string{current.Model}
	}
	defaultModelIndex := "1"
	for index, model := range models {
		if model == current.Model {
			defaultModelIndex = fmt.Sprintf("%d", index+1)
			break
		}
	}
	for index, model := range models {
		fmt.Fprintf(w.out, "  [%d] %s\n", index+1, model)
	}
	modelChoice := w.ask("Choose model", defaultModelIndex)
	selectedModel := models[0]
	if parsed := parseChoice(modelChoice); parsed > 0 && parsed <= len(models) {
		selectedModel = models[parsed-1]
	}
	return config.ModelTarget{Provider: selectedProvider, Model: selectedModel}
}

func (w *Wizard) existingAuth(cfg config.Config, providerID string) *state.AccountAuth {
	providerCfg, ok := cfg.Providers[providerID]
	if !ok || !providerCfg.Enabled {
		return nil
	}
	return w.existingProviderAuth(providerCfg)
}

func (w *Wizard) existingProviderAuth(providerCfg config.ProviderConfig) *state.AccountAuth {
	authFile := providerCfg.AuthFile
	if !providerCfg.UsesProviderAuth() {
		for _, account := range providerCfg.Accounts {
			if providerCfg.ActiveAccountID != "" && account.ID != providerCfg.ActiveAccountID && !account.Active {
				continue
			}
			authFile = account.AuthFile
			break
		}
	}
	if authFile == "" {
		return nil
	}
	auth, err := w.repo.LoadAuth(ResolveAuthPath(w.repo.Layout().Root, authFile))
	if err != nil {
		return nil
	}
	return &auth
}

func (w *Wizard) printSummary(cfg config.Config) {
	fmt.Fprintln(w.out, "\n"+w.paint(colorCyan, "Summary"))
	fmt.Fprintf(w.out, "  Port: %d\n", cfg.Port)
	for _, line := range w.providerSummaryLines(cfg) {
		fmt.Fprintln(w.out, line)
	}
	fmt.Fprintf(w.out, "  Default: %s [%s]\n", cfg.ModelMapping.Default.Model, cfg.ModelMapping.Default.Provider)
	fmt.Fprintf(w.out, "  Opus:    %s [%s]\n", cfg.ModelMapping.Opus.Model, cfg.ModelMapping.Opus.Provider)
	fmt.Fprintf(w.out, "  Sonnet:  %s [%s]\n", cfg.ModelMapping.Sonnet.Model, cfg.ModelMapping.Sonnet.Provider)
	fmt.Fprintf(w.out, "  Haiku:   %s [%s]\n", cfg.ModelMapping.Haiku.Model, cfg.ModelMapping.Haiku.Provider)
}

func (w *Wizard) providerSummaryLines(cfg config.Config) []string {
	lines := []string{}
	providerIDs := currentProviderIDs(cfg)
	for _, providerID := range providerIDs {
		providerCfg := cfg.Providers[providerID]
		label := providerID
		if info, ok := w.providers.Get(providerID); ok {
			label = info.DisplayName
		}
		if providerCfg.UsesProviderAuth() {
			lines = append(lines, fmt.Sprintf("  Provider: %s", label))
			continue
		}
		for _, account := range providerCfg.Accounts {
			status := w.paint(colorGreen, "active")
			if providerCfg.ActiveAccountID != "" && account.ID != providerCfg.ActiveAccountID && !account.Active {
				status = w.paint(colorYellow, "fallback")
			}
			lines = append(lines, fmt.Sprintf("  Provider: %s -> %s (%s)", label, account.Email, status))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "  Provider: none")
	}
	return lines
}

func currentProviderSelection(infos []provider.Info, cfg config.Config) string {
	indexes := []string{}
	for idx, info := range infos {
		if providerCfg, ok := cfg.Providers[info.ID]; ok && providerCfg.Enabled {
			indexes = append(indexes, fmt.Sprintf("%d", idx+1))
		}
	}
	if len(indexes) == 0 {
		return "1"
	}
	return strings.Join(indexes, ",")
}

func currentProviderIDs(cfg config.Config) []string {
	providerIDs := []string{}
	for providerID, providerCfg := range cfg.Providers {
		if providerCfg.Enabled {
			providerIDs = append(providerIDs, providerID)
		}
	}
	sort.Strings(providerIDs)
	return providerIDs
}

func parseChoice(value string) int {
	var parsed int
	fmt.Sscanf(strings.TrimSpace(value), "%d", &parsed)
	return parsed
}

func appendIfMissing(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func cloneAccounts(values []config.AccountRef) []config.AccountRef {
	result := make([]config.AccountRef, len(values))
	copy(result, values)
	return result
}

func activeAccount(providerCfg config.ProviderConfig) config.AccountRef {
	for _, account := range providerCfg.Accounts {
		if account.ID == providerCfg.ActiveAccountID || account.Active {
			return account
		}
	}
	if len(providerCfg.Accounts) > 0 {
		return providerCfg.Accounts[0]
	}
	return config.AccountRef{}
}

func setActiveAccount(providerCfg *config.ProviderConfig, accountID string) {
	if len(providerCfg.Accounts) == 0 {
		providerCfg.ActiveAccountID = ""
		return
	}
	if strings.TrimSpace(accountID) == "" {
		accountID = providerCfg.Accounts[0].ID
	}
	providerCfg.ActiveAccountID = accountID
	for i := range providerCfg.Accounts {
		providerCfg.Accounts[i].Active = providerCfg.Accounts[i].ID == accountID
	}
}

func upsertAccount(providerCfg config.ProviderConfig, account config.AccountRef) config.ProviderConfig {
	for i := range providerCfg.Accounts {
		if providerCfg.Accounts[i].ID == account.ID || strings.EqualFold(providerCfg.Accounts[i].Email, account.Email) {
			providerCfg.Accounts[i] = account
			return providerCfg
		}
	}
	providerCfg.Accounts = append(providerCfg.Accounts, account)
	return providerCfg
}

func removeAccount(providerCfg config.ProviderConfig, accountID string) config.ProviderConfig {
	next := make([]config.AccountRef, 0, len(providerCfg.Accounts))
	for _, account := range providerCfg.Accounts {
		if account.ID != accountID {
			next = append(next, account)
		}
	}
	providerCfg.Accounts = next
	if providerCfg.ActiveAccountID == accountID {
		providerCfg.ActiveAccountID = ""
	}
	setActiveAccount(&providerCfg, providerCfg.ActiveAccountID)
	return providerCfg
}

func (w *Wizard) chooseAccountIndex(prompt string, accounts []config.AccountRef, activeID string) int {
	fallback := "1"
	for i, account := range accounts {
		if account.ID == activeID || account.Active {
			fallback = fmt.Sprintf("%d", i+1)
			break
		}
	}
	value := w.ask(prompt, fallback)
	index := parseChoice(value)
	if index <= 0 || index > len(accounts) {
		return 0
	}
	return index - 1
}

func (w *Wizard) loadAccountAuth(account config.AccountRef) *state.AccountAuth {
	if account.AuthFile == "" {
		return nil
	}
	auth, err := w.repo.LoadAuth(ResolveAuthPath(w.repo.Layout().Root, account.AuthFile))
	if err != nil {
		return nil
	}
	return &auth
}

func (w *Wizard) paint(color string, value string) string {
	return color + value + colorReset
}
