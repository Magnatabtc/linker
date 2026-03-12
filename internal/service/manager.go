package service

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"linker/internal/platform"
	"linker/internal/state"
)

type Manager struct {
	env    platform.Environment
	layout state.Layout
}

func NewManager(env platform.Environment, layout state.Layout) *Manager {
	return &Manager{env: env, layout: layout}
}

func (m *Manager) Install(executable string) (string, error) {
	if err := os.MkdirAll(m.layout.ServiceDir, 0o755); err != nil {
		return "", err
	}
	switch m.env.OS {
	case "darwin":
		path := filepath.Join(m.env.Home, "Library", "LaunchAgents", "com.linker.daemon.plist")
		content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.linker.daemon</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, executable, m.layout.LogFile, m.layout.LogFile)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		return path, os.WriteFile(path, []byte(content), 0o644)
	case "linux":
		dir := filepath.Join(m.env.Home, ".config", "systemd", "user")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		path := filepath.Join(dir, "linker.service")
		content := fmt.Sprintf(`[Unit]
Description=Linker daemon

[Service]
ExecStart=%s serve
Restart=on-failure
WorkingDirectory=%s

[Install]
WantedBy=default.target
`, executable, m.layout.Root)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", err
		}
		if m.env.WSL {
			scriptPath := filepath.Join(m.layout.ServiceDir, "wsl-start-linker.sh")
			script := fmt.Sprintf("#!/usr/bin/env sh\nset -eu\nexec \"%s\" start\n", executable)
			if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
				return "", err
			}
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		return path, nil
	case "windows":
		startup := m.env.StartupDir()
		if startup == "" {
			startup = m.layout.ServiceDir
		}
		if err := os.MkdirAll(startup, 0o755); err != nil {
			return "", err
		}
		path := filepath.Join(startup, "linker.cmd")
		content := fmt.Sprintf("@echo off\r\n\"%s\" start\r\n", executable)
		return path, os.WriteFile(path, []byte(content), 0o644)
	default:
		return "", fmt.Errorf("unsupported OS %s", m.env.OS)
	}
}

func (m *Manager) ServicePath() string {
	switch m.env.OS {
	case "darwin":
		return filepath.Join(m.env.Home, "Library", "LaunchAgents", "com.linker.daemon.plist")
	case "linux":
		return filepath.Join(m.env.Home, ".config", "systemd", "user", "linker.service")
	case "windows":
		startup := m.env.StartupDir()
		if startup == "" {
			startup = m.layout.ServiceDir
		}
		return filepath.Join(startup, "linker.cmd")
	default:
		return ""
	}
}

func (m *Manager) IsInstalled() bool {
	path := m.ServicePath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func (m *Manager) StartInstalled() error {
	if !m.IsInstalled() {
		return os.ErrNotExist
	}
	switch m.env.OS {
	case "darwin":
		path := m.ServicePath()
		domain := launchDomain()
		if err := exec.Command("launchctl", "load", "-w", path).Run(); err != nil {
			_ = exec.Command("launchctl", "bootstrap", domain, path).Run()
		}
		return exec.Command("launchctl", "kickstart", "-k", domain+"/com.linker.daemon").Run()
	case "linux":
		return exec.Command("systemctl", "--user", "enable", "--now", "linker.service").Run()
	case "windows":
		return os.ErrInvalid
	default:
		return os.ErrInvalid
	}
}

func launchDomain() string {
	current, err := user.Current()
	if err != nil || current.Uid == "" {
		return "gui/0"
	}
	return "gui/" + current.Uid
}

func (m *Manager) StopInstalled() error {
	if !m.IsInstalled() {
		return os.ErrNotExist
	}
	switch m.env.OS {
	case "darwin":
		return exec.Command("launchctl", "unload", "-w", m.ServicePath()).Run()
	case "linux":
		return exec.Command("systemctl", "--user", "stop", "linker.service").Run()
	case "windows":
		return os.ErrInvalid
	default:
		return os.ErrInvalid
	}
}

func (m *Manager) InstallHint(path string) string {
	switch m.env.OS {
	case "darwin":
		return fmt.Sprintf("Service file written to %s. Load with launchctl load -w %s", path, path)
	case "linux":
		if m.env.WSL {
			return fmt.Sprintf("Service file written to %s. WSL helper script available at %s. Enable with systemctl --user enable --now linker.service when supported, or call the helper from your shell startup.", path, filepath.Join(m.layout.ServiceDir, "wsl-start-linker.sh"))
		}
		return fmt.Sprintf("Service file written to %s. Enable with systemctl --user enable --now linker.service", path)
	case "windows":
		return fmt.Sprintf("Startup entry written to %s. Linker will start on next sign-in.", path)
	default:
		return path
	}
}

func (m *Manager) Status() string {
	switch runtime.GOOS {
	case "darwin":
		return "launchd user agent"
	case "linux":
		if m.env.WSL {
			return "systemd user unit (WSL-aware)"
		}
		return "systemd user unit"
	case "windows":
		return "Windows Startup folder"
	default:
		return strings.ToUpper(runtime.GOOS)
	}
}
