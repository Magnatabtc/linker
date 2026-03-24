# Linker

<p align="center">
  <img src="./assets/logo/linker-wordmark.png" alt="Linker wordmark" width="760" />
</p>

Linker is a local AI provider bridge for Claude Code. It runs as a local daemon, exposes Anthropic-compatible and OpenAI-compatible endpoints, and routes requests through configured provider accounts.

<p align="center">
  <img src="./assets/logo/linker-mark.png" alt="Linker logo mark" width="120" />
</p>

## At a glance

- Single Go binary
- Local daemon on port `6767`
- Anthropic-compatible `/v1/messages` with Anthropic-style SSE envelopes
- OpenAI-compatible `/v1/chat/completions`
- Model catalog at `/v1/models`
- Interactive `linker onboard`
- Claude Code settings merge into `~/.claude/settings.json`
- Service file generation for macOS, Linux, and Windows
- Gemini CLI OAuth with embedded public desktop credentials, localhost callback, manual callback fallback, and persisted refresh token
- Antigravity OAuth with embedded public desktop credentials, localhost callback, project bootstrap, and request translation to the wrapped Gemini-style upstream format
- Gemini API Key as a separate provider with a persistent key pool, validation, round-robin rotation, cooldown, and CLI management commands
- Codex OAuth with embedded client ID, PKCE, localhost callback, and persisted refresh token
- CodingPlan API-key onboarding against the fixed Anthropic-compatible endpoint

## Quickstart

Use this if you want the quickest start:

```bash
curl -fsSL https://raw.githubusercontent.com/Magnatabtc/linker/main/setup-global.sh -o setup-global.sh && bash setup-global.sh
```

If you already cloned the repo, run the local script instead:

```bash
bash setup-global.sh
```

Then boot Linker with the onboarding wizard:

```bash
linker onboard
```

Choose `QuickStart` for the bootstrap-only path. That path keeps the defaults, wires the provider accounts you select, maps the model slots, and merges the Claude Code settings file for you. Re-run `linker onboard` whenever you want to change providers or remap models.

Start the daemon in the background or foreground:

```bash
linker start
linker start --fg
```

## Install by OS

### macOS

Copy and paste this into your terminal:

```bash
curl -fsSL https://raw.githubusercontent.com/Magnatabtc/linker/main/setup-global.sh -o setup-global.sh && bash setup-global.sh
```

### Linux

Copy and paste this into your terminal:

```bash
curl -fsSL https://raw.githubusercontent.com/Magnatabtc/linker/main/setup-global.sh -o setup-global.sh && bash setup-global.sh
```

### Windows PowerShell

Use this on Windows. Copy and paste this into PowerShell. No Git Bash or WSL needed:

```powershell
$ErrorActionPreference='Stop'; Set-ExecutionPolicy -Scope Process Bypass -Force; Remove-Item -Force .\setup-global.ps1 -ErrorAction SilentlyContinue; iwr https://github.com/Magnatabtc/linker/releases/latest/download/setup-global.ps1 -UseBasicParsing -OutFile setup-global.ps1; .\setup-global.ps1
```

## Copy-Paste Local Setup

```bash
git clone https://github.com/Magnatabtc/linker.git
cd linker

make build

./linker onboard
./linker start --fg
```

There is no dedicated watch/dev-loop command in this repository. After changing code, rerun `make build` to rebuild the binary.

## Configure Providers

Use provider-specific commands when you want to add or manage accounts outside the wizard:

```bash
linker account list
linker account add gemini-cli
linker account add antigravity
linker account add codex
linker account remove <provider> <email>
linker account switch <provider> <email>
linker apikey add gemini
linker apikey list gemini
linker apikey remove gemini <label|index>
linker apikey test gemini
linker apikey stats gemini
```

Notes:

- `gemini-cli`, `antigravity`, and `codex` are OAuth-based accounts.
- `gemini` is the Gemini API Key pool provider.
- `codingplan` is onboarded through `linker onboard` because it uses a fixed Anthropic-compatible endpoint and API-key setup path.

## Configure Models

Model mapping is handled by the onboarding wizard after provider discovery.

- `Default` is the general-purpose slot.
- `Opus`, `Sonnet`, and `Haiku` are the other router slots exposed by the daemon.
- The wizard refreshes the model catalog and lets you map each slot to a discovered provider model.
- Re-run `linker onboard` after adding a new provider if you want to rebuild the mappings from fresh catalog data.

The daemon publishes `/v1/models` once it is running. Use `linker status` to inspect daemon health and `linker config get port` to confirm the bind port:

```bash
linker status
linker config get port
```

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

## Global Installer

This is the quickest path:

```bash
curl -fsSL https://raw.githubusercontent.com/Magnatabtc/linker/main/setup-global.sh -o setup-global.sh && bash setup-global.sh
```

This is the easiest path, but it trusts whatever is currently on `main` when the script is fetched. Use it only if you accept that moving-branch tradeoff.

Safer options:

```bash
curl -fsSL https://raw.githubusercontent.com/linker-cli/linker/v0.1.0/setup-global.sh -o setup-global.sh && bash setup-global.sh
```

or pin to a specific commit SHA if you need an immutable source snapshot. If you publish the installer as a release asset, prefer that release-backed URL over the moving `main` branch.

The existing `install.sh` remains the lower-level installer for advanced or release packaging use.

Release packaging should keep the expected artifact names stable: `linker_windows_amd64.zip`, `linker_linux_amd64.tar.gz`, `linker_linux_arm64.tar.gz`, `linker_darwin_amd64.tar.gz`, `linker_darwin_arm64.tar.gz`, and `checksums.txt`.

What it does:

1. Detects the operating system and CPU architecture.
2. Checks for `curl`, `tar`, checksum tools, and a shell-compatible fallback.
3. Installs missing dependencies when a non-interactive package manager is available.
4. Downloads the latest Linker release and verifies `checksums.txt`.
5. Installs the binary into a writable global directory.
6. Uses `/usr/local/bin` when it is writable, otherwise falls back to `~/.local/bin` or `LINKER_BIN_DIR` if you set one.
7. Adds that directory to the current session PATH and prints a persistent PATH hint when needed.
8. Runs `linker onboard` automatically at the end.

Prerequisites:

- `bash`
- Internet access to GitHub releases
- Linux/macOS: `curl` or `wget`, plus `tar` and `sha256sum` or `shasum`
- Windows: Git Bash, MSYS, or Cygwin, plus `powershell.exe`

Advanced environment variables:

- `LINKER_OWNER` to override the GitHub owner
- `LINKER_REPO` to override the GitHub repository
- `LINKER_VERSION` to pin a release tag such as `v0.1.0`
- `LINKER_BIN_DIR` to force a custom install directory

Troubleshooting:

- If `linker` is not found after install, open a new terminal and add the printed bin directory to PATH. A common example on Unix is `export PATH="$HOME/.local/bin:$PATH"`.
- If the script says a directory is not writable, set `LINKER_BIN_DIR` to a folder you own and rerun the installer.
- If checksum verification fails, rerun the installer and confirm that your network, proxy, or firewall is not changing the download.
- If model mappings look stale, rerun `linker onboard` so the catalog refreshes and the wizard can repopulate the slots.
- On Windows, the script prints a PowerShell PATH example you can paste into a terminal if you want the change to persist.

## Notes

- Claude settings are merged only inside the `env` block and now write `ANTHROPIC_AUTH_TOKEN=linker-local`.
- The Google-related providers are implemented as separate modules with separate auth and upstream behavior: `gemini-cli` uses Google OAuth and the Gemini OpenAI-compatible endpoint; `antigravity` uses a different Google OAuth client, different scopes, and the wrapped `cloudcode-pa.googleapis.com` transport; `gemini-apikey` uses AI Studio API keys and never touches OAuth.
- The current test suite validates the core routing/persistence refactor, but live end-to-end OAuth flows against the upstream services still need real interactive validation on a machine with browser access.
