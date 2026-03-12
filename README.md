# Linker

Linker is a local AI provider bridge for Claude Code. It runs as a local daemon, exposes Anthropic-compatible and OpenAI-compatible endpoints, and routes requests through configured provider accounts.

## Current shape

- Single Go binary
- Local daemon on port `6767`
- Anthropic-compatible `/v1/messages` with Anthropic-style SSE envelopes
- OpenAI-compatible `/v1/chat/completions`
- Model catalog at `/v1/models`
- Interactive `linker onboard`
- Claude Code settings merge into `~/.claude/settings.json`
- Service file generation for macOS, Linux, Windows
- Gemini CLI OAuth with embedded public desktop credentials, localhost callback, manual callback fallback, and persisted refresh token
- Antigravity OAuth with embedded public desktop credentials, localhost callback, project bootstrap, and request translation to the wrapped Gemini-style upstream format
- Gemini API Key as a separate provider with a persistent key pool, validation, round-robin rotation, cooldown, and CLI management commands
- Codex OAuth with embedded client ID, PKCE, localhost callback, and persisted refresh token
- CodingPlan API-key onboarding against the fixed Anthropic-compatible endpoint

## Commands

```bash
linker onboard
linker start
linker start --fg
linker stop
linker restart
linker status
linker logs -f
linker account list
linker account add <provider>
linker account remove <provider> <email>
linker account switch <provider> <email>
linker apikey list gemini
linker apikey add gemini
linker apikey remove gemini <label|index>
linker apikey test gemini
linker apikey stats gemini
linker config get <key>
linker config set <key> <value>
linker version
```

## Development

Use the local Go toolchain bundled in `.tools/go` in this workspace, or any compatible Go 1.26+ install.

```bash
.tools/go/bin/go.exe test ./...
.tools/go/bin/go.exe build ./cmd/linker
```

## Install script

The install script expects GitHub release artifacts named like:

- `linker_windows_amd64.zip`
- `linker_linux_amd64.tar.gz`
- `linker_linux_arm64.tar.gz`
- `linker_darwin_amd64.tar.gz`
- `linker_darwin_arm64.tar.gz`
- `checksums.txt`

It supports the following environment variables:

- `LINKER_OWNER`
- `LINKER_REPO`
- `LINKER_VERSION`
- `LINKER_BIN_DIR`

## Notes

- Claude settings are merged only inside the `env` block and now write `ANTHROPIC_AUTH_TOKEN=linker-local`.
- The three Google-related providers are implemented as separate modules with separate auth and upstream behavior:
  - `gemini-cli` uses Google OAuth and the Gemini OpenAI-compatible endpoint.
  - `antigravity` uses a different Google OAuth client, different scopes, and the wrapped `cloudcode-pa.googleapis.com` transport.
  - `gemini-apikey` uses AI Studio API keys and never touches OAuth.
- The current test suite validates the core routing/persistence refactor, but live end-to-end OAuth flows against the upstream services still need real interactive validation on a machine with browser access.
