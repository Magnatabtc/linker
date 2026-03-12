package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Environment struct {
	OS       string
	Arch     string
	Home     string
	WSL      bool
	SSH      bool
	Headless bool
}

func Detect() Environment {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	env := Environment{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Home:     home,
		WSL:      detectWSL(),
		SSH:      os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "",
		Headless: detectHeadless(runtime.GOOS),
	}
	return env
}

func (e Environment) LinkerDir() string {
	return filepath.Join(e.Home, ".linker")
}

func (e Environment) ClaudeSettingsPath() string {
	return filepath.Join(e.Home, ".claude", "settings.json")
}

func (e Environment) StartupDir() string {
	if e.OS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
		}
	}
	return ""
}

func detectWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	for _, value := range []string{os.Getenv("WSL_DISTRO_NAME"), os.Getenv("WSL_INTEROP")} {
		if value != "" {
			return true
		}
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}

func detectHeadless(goos string) bool {
	switch goos {
	case "linux":
		return os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
	case "darwin":
		return false
	case "windows":
		return false
	default:
		return false
	}
}
