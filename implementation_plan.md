# Linker Implementation Plan

## Objective
- Build `linker` as a greenfield Go project from scratch.
- Deliver a local daemon exposing Anthropic-compatible and OpenAI-compatible endpoints.
- Provide an interactive onboarding wizard, Claude Code auto-configuration, service installation, multi-account storage, model catalog discovery, and provider adapter scaffolding for Gemini CLI, Antigravity, Codex, and CodingPlan.

## Areas

### Area A - Foundation, CLI, runtime, state
- Status: `APPROVED`
- Dependencies: none
- Files:
  - `go.mod`
  - `cmd/linker/**`
  - `internal/app/**`
  - `internal/config/**`
  - `internal/state/**`
  - `internal/runtime/**`
  - `internal/service/**`
  - `internal/platform/**`
  - `internal/logging/**`
  - `.github/workflows/**`
  - `Makefile`
- Acceptance:
  - Project builds as a single Go binary.
  - CLI commands exist and wire into application services.
  - Config, auth store, model registry, PID file, and log directory are created with atomic persistence.
  - Runtime lifecycle supports `start`, `stop`, `restart`, `status`, and `logs`.
  - Cross-platform service integration exists for macOS, Linux, Windows, and WSL-aware Linux behavior.
  - Unit tests cover persistence, lifecycle helpers, and command wiring.

### Area B - Compatibility gateway and routing
- Status: `APPROVED`
- Dependencies: Area A contracts available
- Files:
  - `internal/api/**`
  - `internal/compat/**`
  - `internal/router/**`
- Acceptance:
  - `/v1/messages`, `/v1/chat/completions`, `/v1/models`, `/healthz`, and `/readyz` exist.
  - Anthropic and OpenAI requests normalize into one internal request model.
  - Streaming SSE/chunk responses work for both compatibility surfaces.
  - Tool calling and image content shapes are preserved in the internal model.
  - Routing resolves Claude slot aliases and direct configured models.
  - Unit and integration tests cover happy path plus at least two error paths per endpoint.

### Area C - Onboarding, Claude integration, install/distribution
- Status: `APPROVED`
- Dependencies: Area A contracts available, Area D model discovery surface available
- Files:
  - `internal/onboard/**`
  - `internal/claude/**`
  - `install.sh`
  - `install/**`
  - `Formula/**`
  - `README.md`
  - `logo.txt`
- Acceptance:
  - `linker onboard` supports QuickStart and Advanced flows.
  - Existing config can be kept, changed, or reset.
  - Claude settings merge is previewed before write and preserves unrelated keys.
  - Install script downloads the right release artifact and launches onboarding.
  - Homebrew formula and release packaging instructions are present.
  - Tests cover settings merge, wizard defaults, and install-script detection logic.

### Area D - Provider adapters, auth, model catalog
- Status: `APPROVED`
- Dependencies: Area A contracts available
- Files:
  - `internal/provider/**`
  - `internal/catalog/**`
  - `internal/providerkit/**`
  - `internal/router/**`
  - `internal/api/**`
  - `internal/config/**`
  - `internal/state/**`
- Acceptance:
  - Shared provider adapter contract exists.
- Provider storage cleanly separates `oauth`, `apikey`, and `apikey-pool` flows.
- Gemini CLI uses embedded public desktop OAuth credentials with localhost callback and silent refresh.
- Antigravity uses its own embedded Google OAuth credentials, scopes, project bootstrap, and request wrapper.
- Gemini API Key exists as a separate provider with key-pool rotation, cooldown, validation, and stats.
- Codex uses embedded OAuth client settings with localhost callback and silent refresh.
- CodingPlan uses direct API-key auth against the fixed Anthropic-compatible endpoint.
- Multi-account persistence and active-account switching work.
- Model discovery populates a cacheable registry by provider/account.
- Unit and integration tests cover auth persistence, discovery, and failover behavior.

## Execution Order
1. Area A worker -> reviewer -> approved
2. Area D worker -> reviewer -> approved
3. Area B and Area C workers in parallel -> reviewers -> approved
4. General reviewer for integration, build, tests, and packaging checks

## Current State
- Foundation, routing, onboarding, Claude settings merge, daemon lifecycle, and packaging scaffolding are implemented and validated locally.
- The provider surface now supports account-scoped oauth, provider-scoped API keys, and API-key pools.
- The audit closure cycle landed:
  - auth files for OAuth providers now persist with email-based filenames such as `gemini-cli_user@example.com.json`
  - onboarding now supports keep/change/start-fresh plus add/remove provider and add/remove/switch/re-authenticate account flows
  - `status` now reports uptime and enabled providers
  - WSL installation now writes a startup helper script alongside the user service files
  - account routing now rotates OAuth accounts automatically and falls back on `401`, `403`, and `429`
  - Antigravity now uses both `generateContent` and `streamGenerateContent`, with native stream conversion into Anthropic/OpenAI SSE output
- Remaining validation work is interactive end-to-end browser/OAuth verification against the live upstream services.

## Test Strategy
- Unit tests for all new exported behavior.
- Integration tests for gateway normalization/routing and provider/account interactions.
- E2E CLI tests for onboarding and service lifecycle.
- Final system validation: build, unit tests, integration tests, and CLI smoke checks.

## Final Gate
- All areas `APPROVED`
- Local validation gate passed: `go build`, `go test`, `go vet`, `go run ./cmd/linker help`, daemon smoke checks
- `walkthrough.md` created with execution log
