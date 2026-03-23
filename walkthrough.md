# Linker Walkthrough

## Summary
- The repository started effectively empty.
- The implementation was bootstrapped as a greenfield Go project.
- Subagent execution was attempted first, but the delegated worker backend could not edit files or run commands in its environment.
- The implementation then continued locally with the same area split defined in `implementation_plan.md`.

## Delivered
- Go module and single-binary CLI entrypoint
- Config store, state repository, PID/log/model-registry persistence
- Runtime supervisor with `start`, `stop`, `restart`, `status`, and `logs`
- Service file generation plus `systemd`/`launchd` start-stop integration
- Provider registry backed by per-provider modules under `internal/providers/*`
- Real Google OAuth localhost callback flows for Gemini CLI and Antigravity with persisted refresh tokens
- Separate Gemini API Key provider with persistent key pool, validation, rotation, cooldown, and CLI management
- Codex OAuth with embedded client ID + PKCE
- CodingPlan fixed-endpoint API-key flow
- Anthropic-compatible and OpenAI-compatible HTTP surfaces with improved Anthropic SSE and tool mapping
- Claude Code settings merge helpers with explicit diff preview and `linker-local` token
- **TUI Redesign**: Replaced legacy text-based onboarding with a modern, interactive Terminal User Interface (TUI) based on the **Charmbracelet** stack (`huh` and `lipgloss`), achieving parity with OpenClaw design standards.
- **Linker Dashboard (TUI)**: A new full-screen dashboard (`linker tui`) for real-time monitoring of daemon health, provider status, and active accounts, featuring a responsive, tabbed interface.
- **Design System**: Created `DESIGN.md` to establish and maintain high-quality UI/UX standards for the project.
- Install script with release lookup and checksum validation, Homebrew formula template, CI workflow, README, and logo asset
- Unit tests for core persistence, merge, parsing, and routing contracts
- Audit-driven closure for re-onboard account management, OAuth account fallback rotation, Antigravity native streaming, WSL startup helper generation, and status uptime/provider reporting

## Validation
- `go test ./...`
- `go vet ./...`
- `go build ./cmd/linker`
- `go run ./cmd/linker version`
- `go run ./cmd/linker help`
- `linker.exe start`
- `curl http://127.0.0.1:6767/healthz`
- `linker.exe status`
- `linker.exe stop`
- hidden workspace hygiene search for forbidden external project names

## Notes
- Gemini CLI now authenticates through Google's OAuth authorization-code flow with embedded desktop credentials on `localhost:8085`.
- Antigravity now authenticates through its own Google OAuth client on `localhost:51121`, then resolves or bootstraps the project id required by the upstream gateway.
- Gemini API Key is isolated from the OAuth-based Google providers and stores a pool of AI Studio keys with round-robin failover.
- Codex now uses embedded OAuth + PKCE on `localhost:1455`.
- Live browser-based OAuth handshakes and live upstream generation still need manual end-to-end verification on a machine with browser access; the current validation in this workspace covered build, tests, and daemon/runtime smoke checks.
- The temporary external reference checkout used during research was removed from the workspace before final audit.
