package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"linker/internal/config"
	"linker/internal/provider"
	"linker/internal/service"
)

type sessionState int

const (
	stateStatus sessionState = iota
	stateProviders
	stateLogs
)

var (
	styleTitle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FFFF")).Bold(true).Padding(0, 1).MarginRight(2).Border(lipgloss.RoundedBorder())
	styleTab      = lipgloss.NewStyle().Padding(0, 1).MarginRight(1)
	styleActiveTab = styleTab.Copy().Foreground(lipgloss.Color("#00FFFF")).Underline(true)
	styleFooter   = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080")).Faint(true)
	styleContent  = lipgloss.NewStyle().Padding(1, 2).Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(lipgloss.Color("#404040"))
	styleStatus   = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	styleError    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
)

type Dashboard struct {
	cfg       config.Config
	providers *provider.Registry
	services  *service.Manager
	state     sessionState
	width     int
	height    int
}

func NewDashboard(cfg config.Config, providers *provider.Registry, services *service.Manager) *Dashboard {
	return &Dashboard{
		cfg:       cfg,
		providers: providers,
		services:  services,
		state:     stateStatus,
	}
}

func (m *Dashboard) Init() tea.Cmd {
	return nil
}

func (m *Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "1":
			m.state = stateStatus
		case "2":
			m.state = stateProviders
		case "3":
			m.state = stateLogs
		case "tab":
			m.state = (m.state + 1) % 3
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}
	return m, nil
}

func (m *Dashboard) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing dashboard..."
	}

	header := lipgloss.JoinHorizontal(lipgloss.Top,
		styleTitle.Render("LINKER DASHBOARD"),
		m.renderTab("1. Status", m.state == stateStatus),
		m.renderTab("2. Providers", m.state == stateProviders),
		m.renderTab("3. Logs", m.state == stateLogs),
	)

	content := ""
	switch m.state {
	case stateStatus:
		content = m.viewStatus()
	case stateProviders:
		content = m.viewProviders()
	case stateLogs:
		content = m.viewLogs()
	}

	footer := styleFooter.Render(" [1,2,3/Tab] Switch Tab • [q] Quit")

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		styleContent.Width(m.width-4).Height(m.height-6).Render(content),
		footer,
	)
}

func (m *Dashboard) renderTab(label string, active bool) string {
	if active {
		return styleActiveTab.Render(label)
	}
	return styleTab.Render(label)
}

func (m *Dashboard) viewStatus() string {
	s := "DAEMON STATUS\n\n"
	status := "Running" // In real implementation, check actual service state
	s += fmt.Sprintf("  Status:  %s\n", styleStatus.Render(status))
	s += fmt.Sprintf("  Port:    %d\n", m.cfg.Port)
	s += fmt.Sprintf("  Config:  %s\n", m.cfg.ClaudeCode.SettingsPath)
	return s
}

func (m *Dashboard) viewProviders() string {
	s := "ACTIVE PROVIDERS\n\n"
	for id, providerCfg := range m.cfg.Providers {
		if !providerCfg.Enabled {
			continue
		}
		label := id
		if info, ok := m.providers.Get(id); ok {
			label = info.DisplayName
		}
		s += fmt.Sprintf("  • %s\n", label)
		if !providerCfg.UsesProviderAuth() {
			for _, account := range providerCfg.Accounts {
				status := "Active"
				if !account.Active {
					status = "Fallback"
				}
				s += fmt.Sprintf("    - %s (%s)\n", account.Email, status)
			}
		}
	}
	return s
}

func (m *Dashboard) viewLogs() string {
	return "LOG STREAM (TBD)\n\n  Streaming logs will be displayed here."
}
