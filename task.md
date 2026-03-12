# Linker Task Tracker

## Area Status
- Area A - Foundation, CLI, runtime, state: `APPROVED`
- Area B - Compatibility gateway and routing: `APPROVED`
- Area C - Onboarding, Claude integration, install/distribution: `APPROVED`
- Area D - Provider adapters, auth, model catalog: `APPROVED`

## Cycle Log
- Area A - Cycle 1 - Subagent blocked by backend sandbox mismatch; fallback execution completed locally
- Area B - Anthropic normalization and SSE were tightened for `tool_use`, `tool_result`, and Anthropic event sequencing; verified by `go test ./...`
- Area C - Claude settings merge now manages only `env`, previews a diff, and writes `ANTHROPIC_AUTH_TOKEN=linker-local`; install and CI packaging were updated to use release assets plus `checksums.txt`
- Area D - Current cycle reopened. The provider layer still assumes account-scoped auth for every provider and must now be refactored to support three distinct auth classes: `oauth`, `apikey`, and `apikey-pool`
- Area D - Gemini CLI credentials were confirmed from a working public desktop OAuth client and must be embedded directly in the binary
- Area D - Antigravity credentials, scopes, callback port, and upstream bootstrap flow were confirmed and now move from stub to implementation
- Area D - Gemini API Key is being added as a separate provider with a persistent key pool, validation, cooldown, and round-robin failover
- Area D - Codex is moving from manual API key to OAuth callback flow; CodingPlan is moving to fixed endpoint API-key auth
- Area D - The refactor landed: provider configs now support account-scoped oauth and provider-scoped `auth_file` flows in parallel
- Area D - Gemini CLI now uses embedded Google desktop OAuth credentials on `localhost:8085` with silent refresh and no user env vars
- Area D - Antigravity now uses its own embedded Google desktop OAuth credentials on `localhost:51121`, fetches/bootstrap the project id, and translates normalized requests into the wrapped upstream request format
- Area D - Gemini API Key is now a first-class provider with `linker apikey list|add|remove|test|stats gemini`
- Area D - Codex now authenticates through embedded OAuth + PKCE on `localhost:1455`; CodingPlan now uses the fixed Alibaba Anthropic endpoint
- Runtime - `start`, `stop`, `restart`, and `onboard` now prefer installed `systemd`/`launchd` services before raw background PID control
- Final validation - `go test ./...`, `go vet ./...`, `go build ./cmd/linker`, `go run ./cmd/linker help`, daemon smoke checks on `/healthz`, `status`, and `stop`
- Audit closure - removed the temporary technical-reference clone from `.tmp`, added email-based OAuth auth filenames, expanded re-onboard account/provider management, added status uptime/provider output, added WSL startup helper generation, implemented OAuth account round-robin/fallback, and wired native Antigravity streaming into the server
