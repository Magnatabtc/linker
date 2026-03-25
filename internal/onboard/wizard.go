package onboard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"linker/internal/catalog"
	"linker/internal/claude"
	"linker/internal/config"
	"linker/internal/platform"
	"linker/internal/provider"
	"linker/internal/providerkit"
	"linker/internal/service"
	"linker/internal/state"
)

var (
	stylePrimary = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FFFF")).Bold(true)
	styleSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	styleWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFF00"))
	styleError   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	styleMuted   = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	styleHeader  = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FFFF")).Bold(true).Padding(0, 1).MarginBottom(1).Border(lipgloss.RoundedBorder())
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

	fmt.Fprintln(w.out, styleHeader.Render("Welcome to Linker!"))
	fmt.Fprintln(w.out, "  Your local AI bridge for Claude Code.")

	choice, err := w.instantSelect(
		"Choose setup mode",
		"QuickStart keeps daemon defaults, while Advanced lets you tune the daemon port before provider and model setup.",
		[]string{"QuickStart", "Advanced"},
	)
	if err != nil {
		return config.Config{}, err
	}
	advanced := choice == 1
	if advanced {
		cfg.Port = w.askInt("Daemon port", cfg.Port)
	}
	if cfg.ClaudeCode.SettingsPath == "" {
		cfg.ClaudeCode = w.defaultCfg.ClaudeCode
	}

	selectedProviders, err := w.providerSelectionFlow(cfg)
	if err != nil {
		return config.Config{}, err
	}
	cfg.Providers, err = w.configureSelectedProviders(ctx, cfg, selectedProviders)
	if err != nil {
		return config.Config{}, err
	}

	registry, err := w.catalog.Refresh(ctx, cfg)
	if err != nil {
		fmt.Fprintf(w.out, "%s\n", styleWarning.Render("! Catalog refresh failed, using built-in models only: "+err.Error()))
		registry = state.ModelRegistry{Entries: map[string][]state.DiscoveredModel{}}
	}
	w.configureMappings(&cfg, registry)

	w.printSummary(cfg)
	if !w.askConfirm("Continue with this configuration?", true) {
		return config.Config{}, fmt.Errorf("onboarding cancelled")
	}

	env := claude.DesiredEnv(cfg)
	settingsPath := cfg.ClaudeCode.SettingsPath
	diff, preview, err := claude.Preview(settingsPath, env)
	if err != nil {
		return config.Config{}, err
	}

	fmt.Fprintf(w.out, "\n%s\n", styleHeader.Render("Claude Settings Update"))
	fmt.Fprintf(w.out, "  Path: %s\n\n", stylePrimary.Render(settingsPath))

	for _, line := range diff {
		if strings.HasPrefix(line, "+") {
			fmt.Fprintln(w.out, styleSuccess.Render("  "+line))
		} else if strings.HasPrefix(line, "-") {
			fmt.Fprintln(w.out, styleError.Render("  "+line))
		} else {
			fmt.Fprintln(w.out, "  "+line)
		}
	}
	fmt.Fprintln(w.out)

	if w.askConfirm("Apply changes to "+settingsPath+"?", true) {
		if err := claude.Save(settingsPath, preview); err != nil {
			return config.Config{}, err
		}
		fmt.Fprintln(w.out, styleSuccess.Render("✔ Claude Code settings updated."))
	}

	if err := w.store.Save(cfg); err != nil {
		return config.Config{}, err
	}
	fmt.Fprintln(w.out, styleSuccess.Render("✔ Linker configuration saved."))
	fmt.Fprintln(w.out)
	fmt.Fprintln(w.out, styleHeader.Render("Next Steps"))
	fmt.Fprintln(w.out, "  1. Start Linker with `linker start` once setup is complete.")
	fmt.Fprintln(w.out, "  2. Run `linker status` to verify the daemon and provider summary.")
	fmt.Fprintln(w.out, "  3. Re-run `linker onboard` any time you need to change providers or Claude slots.")

	executable, _ := os.Executable()
	if executable != "" {
		if path, err := w.services.Install(executable); err == nil {
			fmt.Fprintln(w.out, styleSuccess.Render("✔ Service installed: "+path))
			fmt.Fprintln(w.out, styleMuted.Render(w.services.InstallHint(path)))
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
	if len(currentProviderIDs(cfg)) == 0 {
		return w.selectProviders(cfg)
	}

	fmt.Fprintln(w.out, styleWarning.Render("\nExisting configuration detected."))
	for _, line := range w.providerSummaryLines(cfg) {
		fmt.Fprintln(w.out, line)
	}

	choice, err := w.instantSelect(
		"Existing Providers",
		"What would you like to do with your existing providers?",
		[]string{"Keep existing", "Add provider", "Remove provider", "Change providers", "Start fresh"},
	)
	if err != nil {
		return nil, err
	}

	switch choice {
	case 0: // Keep
		selected := currentProviderIDs(cfg)
		if len(selected) == 0 {
			return w.selectProviders(cfg)
		}
		return selected, nil
	case 1: // Add
		return w.addProviders(cfg)
	case 2: // Remove
		return w.removeProviders(cfg)
	case 4: // Start fresh
		return w.selectProviders(config.Default(cfg.ClaudeCode.SettingsPath))
	default:
		return w.selectProviders(cfg)
	}
}

func (w *Wizard) addProviders(cfg config.Config) ([]string, error) {
	current := currentProviderIDs(cfg)
	available := []huh.Option[string]{}
	for _, info := range w.providers.List() {
		if _, ok := cfg.Providers[info.ID]; ok && cfg.Providers[info.ID].Enabled {
			continue
		}
		available = append(available, huh.NewOption(info.DisplayName, info.ID))
	}
	if len(available) == 0 {
		return current, nil
	}

	var toAdd []string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Add Provider(s)").
				Description("Select the additional providers you want to enable.").
				Options(available...).
				Value(&toAdd),
		),
	).Run()
	if err != nil {
		return current, err
	}

	for _, providerID := range toAdd {
		current = appendIfMissing(current, providerID)
	}
	sort.Strings(current)
	return current, nil
}

func (w *Wizard) removeProviders(cfg config.Config) ([]string, error) {
	current := currentProviderIDs(cfg)
	if len(current) == 0 {
		return w.selectProviders(cfg)
	}

	options := []huh.Option[string]{}
	for _, providerID := range current {
		label := providerID
		if info, ok := w.providers.Get(providerID); ok {
			label = info.DisplayName
		}
		options = append(options, huh.NewOption(label, providerID))
	}

	var toRemove []string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Remove Provider(s)").
				Description("Select the providers you want to remove from your configuration.").
				Options(options...).
				Value(&toRemove),
		),
	).Run()
	if err != nil {
		return current, err
	}

	removeMap := make(map[string]bool)
	for _, r := range toRemove {
		removeMap[r] = true
	}

	selected := []string{}
	for _, providerID := range current {
		if !removeMap[providerID] {
			selected = append(selected, providerID)
		}
	}
	if len(selected) == 0 {
		return w.selectProviders(cfg)
	}
	return selected, nil
}

func (w *Wizard) selectProviders(cfg config.Config) ([]string, error) {
	infos := w.providers.List()
	options := make([]huh.Option[string], len(infos))
	currentIDs := currentProviderIDs(cfg)
	for i, info := range infos {
		options[i] = huh.NewOption(info.DisplayName, info.ID)
		for _, cur := range currentIDs {
			if cur == info.ID {
				options[i] = options[i].Selected(true)
			}
		}
	}

	var selected []string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("AI Providers").
				Description("Choose your AI provider(s). Use Space to select, Enter to confirm.").
				Options(options...).
				Value(&selected),
		),
	).Run()
	if err != nil {
		return nil, err
	}

	if len(selected) == 0 {
		return nil, fmt.Errorf("at least one provider must be selected")
	}
	sort.Strings(selected)
	return selected, nil
}

func (w *Wizard) captureAccount(ctx context.Context, providerID string, existing *state.AccountAuth) (state.AccountAuth, string, provider.Info, error) {
	info, _ := w.providers.Get(providerID)
	fmt.Fprintf(w.out, "\n%s\n", stylePrimary.Render("Authenticating "+info.DisplayName))

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
	fmt.Fprintf(w.out, "%s\n", styleSuccess.Render("✔ Authentication completed for "+info.DisplayName))
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
		fmt.Fprintf(w.out, "\n%s\n", styleWarning.Render("Existing accounts for "+providerID))
		for idx, account := range providerCfg.Accounts {
			status := styleMuted.Render("fallback")
			if account.Active || providerCfg.ActiveAccountID == account.ID {
				status = styleSuccess.Render("active")
			}
			fmt.Fprintf(w.out, "  %d. %s (%s)\n", idx+1, account.Email, status)
		}

		choice, err := w.instantSelect(
			"Account Management",
			"Manage your accounts for "+providerID,
			[]string{"Keep existing", "Switch active account", "Add account", "Remove account", "Re-authenticate active"},
		)
		if err != nil {
			return providerCfg, err
		}

		switch choice {
		case 0: // Keep
			setActiveAccount(&providerCfg, providerCfg.ActiveAccountID)
			return providerCfg, nil
		case 1: // Switch
			index := w.chooseAccountIndex("Choose active account", providerCfg.Accounts, providerCfg.ActiveAccountID)
			setActiveAccount(&providerCfg, providerCfg.Accounts[index].ID)
		case 2: // Add
			auth, authPath, _, err := w.captureAccount(ctx, providerID, nil)
			if err != nil {
				return config.ProviderConfig{}, err
			}
			providerCfg = upsertAccount(providerCfg, config.AccountRef{ID: auth.ID, Email: auth.Email, Active: false, AuthFile: authPath})
			if w.askConfirm("Set this account as active?", false) {
				setActiveAccount(&providerCfg, auth.ID)
			}
		case 3: // Remove
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
		case 4: // Re-auth
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
	providerOrder, modelsByProvider := w.mappingChoices(*cfg, registry)

	fmt.Fprintln(w.out, styleHeader.Render("Model Mappings"))
	cfg.ModelMapping.Default = w.chooseModelTarget("Default model", cfg.ModelMapping.Default, providerOrder, modelsByProvider)
	cfg.ModelMapping.Opus = w.chooseModelTarget("Opus slot", cfg.ModelMapping.Opus, providerOrder, modelsByProvider)
	cfg.ModelMapping.Sonnet = w.chooseModelTarget("Sonnet slot", cfg.ModelMapping.Sonnet, providerOrder, modelsByProvider)
	cfg.ModelMapping.Haiku = w.chooseModelTarget("Haiku slot", cfg.ModelMapping.Haiku, providerOrder, modelsByProvider)
}

func (w *Wizard) mappingChoices(cfg config.Config, registry state.ModelRegistry) ([]string, map[string][]string) {
	modelsByProvider := map[string][]string{}
	providerOrder := []string{}

	for providerID, providerCfg := range cfg.Providers {
		if !providerCfg.Enabled {
			continue
		}
		providerOrder = append(providerOrder, providerID)
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
	sort.Strings(providerOrder)
	return providerOrder, modelsByProvider
}

func (w *Wizard) ask(title string, fallback string) string {
	var result string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(title).
				Placeholder(fallback).
				Value(&result),
		),
	).Run()
	if err != nil || result == "" {
		return fallback
	}
	return result
}

func (w *Wizard) askConfirm(title string, defaultValue bool) bool {
	var result bool
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(title).
				Value(&result).
				Affirmative("Yes").
				Negative("No"),
		),
	).Run()
	if err != nil {
		return defaultValue
	}
	return result
}

func (w *Wizard) askInt(title string, fallback int) int {
	var result string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(title).
				Placeholder(strconv.Itoa(fallback)).
				Validate(func(s string) error {
					if s == "" {
						return nil
					}
					_, err := strconv.Atoi(s)
					if err != nil {
						return fmt.Errorf("must be a number")
					}
					return nil
				}).
				Value(&result),
		),
	).Run()
	if err != nil || result == "" {
		return fallback
	}
	val, _ := strconv.Atoi(result)
	return val
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
	providerLabels := make([]string, len(providerOrder))
	for i, providerID := range providerOrder {
		label := providerID
		if info, ok := w.providers.Get(providerID); ok {
			label = info.DisplayName
		}
		providerLabels[i] = label
	}

	providerIdx := optionIndex(providerOrder, current.Provider)
	providerIdx, err := w.instantSelectWithDefault(prompt+": Choose Provider", "", providerLabels, providerIdx)
	if err != nil {
		providerIdx = optionIndex(providerOrder, current.Provider)
	}
	selectedProvider := providerOrder[providerIdx]

	models := modelsByProvider[selectedProvider]
	if len(models) == 0 {
		models = []string{current.Model}
	}

	modelIdx := optionIndex(models, current.Model)
	modelIdx, err = w.instantSelectWithDefault(prompt+": Choose Model", "", models, modelIdx)
	if err != nil {
		modelIdx = optionIndex(models, current.Model)
	}
	selectedModel := models[modelIdx]

	return config.ModelTarget{Provider: selectedProvider, Model: selectedModel}
}

func optionIndex(options []string, current string) int {
	for idx, option := range options {
		if option == current {
			return idx
		}
	}
	return 0
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
	fmt.Fprintln(w.out, "\n"+styleHeader.Render("Configuration Summary"))
	fmt.Fprintf(w.out, "  Port: %s\n", stylePrimary.Render(strconv.Itoa(cfg.Port)))
	for _, line := range w.providerSummaryLines(cfg) {
		fmt.Fprintln(w.out, line)
	}
	fmt.Fprintf(w.out, "  Default: %s [%s]\n", stylePrimary.Render(cfg.ModelMapping.Default.Model), styleMuted.Render(cfg.ModelMapping.Default.Provider))
	fmt.Fprintf(w.out, "  Opus:    %s [%s]\n", stylePrimary.Render(cfg.ModelMapping.Opus.Model), styleMuted.Render(cfg.ModelMapping.Opus.Provider))
	fmt.Fprintf(w.out, "  Sonnet:  %s [%s]\n", stylePrimary.Render(cfg.ModelMapping.Sonnet.Model), styleMuted.Render(cfg.ModelMapping.Sonnet.Provider))
	fmt.Fprintf(w.out, "  Haiku:   %s [%s]\n", stylePrimary.Render(cfg.ModelMapping.Haiku.Model), styleMuted.Render(cfg.ModelMapping.Haiku.Provider))
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
			lines = append(lines, fmt.Sprintf("  • Provider: %s", stylePrimary.Render(label)))
			continue
		}
		for _, account := range providerCfg.Accounts {
			status := styleSuccess.Render("active")
			if providerCfg.ActiveAccountID != "" && account.ID != providerCfg.ActiveAccountID && !account.Active {
				status = styleWarning.Render("fallback")
			}
			lines = append(lines, fmt.Sprintf("  • Provider: %s → %s (%s)", stylePrimary.Render(label), account.Email, status))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "  • Provider: none")
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

type selectionModel struct {
	title       string
	description string
	options     []string
	cursor      int
	choice      int
	quitting    bool
}

func (m selectionModel) Init() tea.Cmd {
	return nil
}

func (m selectionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.quitting = true
			m.choice = -1
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.options)-1 {
				m.cursor++
			}
		case "enter":
			m.choice = m.cursor
			return m, tea.Quit
		default:
			if i, err := strconv.Atoi(msg.String()); err == nil {
				if i > 0 && i <= len(m.options) {
					m.choice = i - 1
					return m, tea.Quit
				}
			}
		}
	}
	return m, nil
}

func (m selectionModel) View() string {
	if m.quitting {
		return ""
	}
	s := "\n" + styleHeader.Render(m.title) + "\n"
	if m.description != "" {
		s += styleMuted.Render(m.description) + "\n\n"
	} else {
		s += "\n"
	}

	for i, option := range m.options {
		cursor := "  "
		style := lipgloss.NewStyle()
		if m.cursor == i {
			cursor = stylePrimary.Render("> ")
			style = stylePrimary
		}

		s += fmt.Sprintf("%s [%d] %s\n", cursor, i+1, style.Render(option))
	}

	s += "\n" + styleMuted.Render("(use arrows or press a number to select)") + "\n"
	return s
}

func (w *Wizard) instantSelect(title, description string, options []string) (int, error) {
	return w.instantSelectWithDefault(title, description, options, 0)
}

func (w *Wizard) instantSelectWithDefault(title, description string, options []string, defaultCursor int) (int, error) {
	if defaultCursor < 0 || defaultCursor >= len(options) {
		defaultCursor = 0
	}
	m := selectionModel{
		title:       title,
		description: description,
		options:     options,
		cursor:      defaultCursor,
		choice:      -1,
	}
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return -1, err
	}
	result := finalModel.(selectionModel)
	if result.choice == -1 {
		return -1, fmt.Errorf("selection cancelled")
	}
	return result.choice, nil
}
