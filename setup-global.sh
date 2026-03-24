#!/usr/bin/env bash
set -euo pipefail

OWNER="${LINKER_OWNER:-linker-cli}"
REPO="${LINKER_REPO:-linker}"
VERSION="${LINKER_VERSION:-}"
BIN_DIR_OVERRIDE="${LINKER_BIN_DIR:-}"
TAR_CMD=""
SHA256_CMD=""
SHA256_ARGS=()
INSTALLED_VERSION=""

log() {
  printf '%s\n' "$*"
}

warn() {
  printf 'Warning: %s\n' "$*" >&2
}

die() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

have() {
  command -v "$1" >/dev/null 2>&1
}

need_any_downloader() {
  have curl || have wget
}

need_sha256_tool() {
  have sha256sum || have gsha256sum || have shasum
}

resolve_tar_cmd() {
  if have tar; then
    TAR_CMD="tar"
    return 0
  fi

  if have gtar; then
    TAR_CMD="gtar"
    return 0
  fi

  TAR_CMD=""
  return 1
}

resolve_sha256_cmd() {
  SHA256_CMD=""
  SHA256_ARGS=()

  if have sha256sum; then
    SHA256_CMD="sha256sum"
    return 0
  fi

  if have gsha256sum; then
    SHA256_CMD="gsha256sum"
    return 0
  fi

  if have shasum; then
    SHA256_CMD="shasum"
    SHA256_ARGS=(-a 256)
    return 0
  fi

  return 1
}

install_go_toolchain() {
  if have go; then
    return 0
  fi

  log "Go toolchain is missing; attempting automatic installation..."

  if have brew; then
    brew install go
    hash -r || true
    return 0
  fi

  if have apt-get; then
    if have sudo && [[ "$(id -u)" != "0" ]]; then
      if sudo -n true >/dev/null 2>&1; then
        sudo apt-get update
        sudo apt-get install -y golang-go
        hash -r || true
        return 0
      fi
    elif [[ "$(id -u)" == "0" ]]; then
      apt-get update
      apt-get install -y golang-go
      hash -r || true
      return 0
    fi
  fi

  if have dnf; then
    if have sudo && [[ "$(id -u)" != "0" ]]; then
      if sudo -n true >/dev/null 2>&1; then
        sudo dnf install -y golang
        hash -r || true
        return 0
      fi
    elif [[ "$(id -u)" == "0" ]]; then
      dnf install -y golang
      hash -r || true
      return 0
    fi
  fi

  if have yum; then
    if have sudo && [[ "$(id -u)" != "0" ]]; then
      if sudo -n true >/dev/null 2>&1; then
        sudo yum install -y golang
        hash -r || true
        return 0
      fi
    elif [[ "$(id -u)" == "0" ]]; then
      yum install -y golang
      hash -r || true
      return 0
    fi
  fi

  if have pacman; then
    if have sudo && [[ "$(id -u)" != "0" ]]; then
      if sudo -n true >/dev/null 2>&1; then
        sudo pacman -Sy --noconfirm go
        hash -r || true
        return 0
      fi
    elif [[ "$(id -u)" == "0" ]]; then
      pacman -Sy --noconfirm go
      hash -r || true
      return 0
    fi
  fi

  if have apk; then
    if [[ "$(id -u)" == "0" ]]; then
      apk add go
      hash -r || true
      return 0
    fi
    if have sudo && sudo -n true >/dev/null 2>&1; then
      sudo apk add go
      hash -r || true
      return 0
    fi
  fi

  if is_windows_shell; then
    if have winget; then
      ps_eval "winget install --exact --id GoLang.Go --accept-source-agreements --accept-package-agreements"
      hash -r || true
      return 0
    fi

    if have choco; then
      ps_eval "choco install golang -y --no-progress"
      hash -r || true
      return 0
    fi
  fi

  warn "Automatic Go installation was skipped because no supported package manager was available."
  return 1
}

install_missing_unix_tools() {
  local downloader_pkg=""
  local tar_pkg=""
  local checksum_pkg=""
  local packages=()

  if need_any_downloader; then
    :
  else
    downloader_pkg="curl"
  fi

  if resolve_tar_cmd; then
    :
  else
    tar_pkg="tar"
  fi

  if resolve_sha256_cmd; then
    :
  else
    checksum_pkg="coreutils"
  fi

  if [[ -n "${downloader_pkg}" ]]; then
    packages+=("${downloader_pkg}")
  fi
  if [[ -n "${tar_pkg}" ]]; then
    packages+=("${tar_pkg}")
  fi
  if [[ -n "${checksum_pkg}" ]]; then
    packages+=("${checksum_pkg}")
  fi

  if [[ "${#packages[@]}" -eq 0 ]]; then
    return 0
  fi

  log "Some tools are missing: ${packages[*]}"

  if have apt-get; then
    if have sudo && [[ "$(id -u)" != "0" ]]; then
      if sudo -n true >/dev/null 2>&1; then
        sudo apt-get update
        sudo apt-get install -y "${packages[@]}"
        return 0
      fi
    elif [[ "$(id -u)" == "0" ]]; then
      apt-get update
      apt-get install -y "${packages[@]}"
      return 0
    fi
  fi

  if have dnf; then
    if have sudo && [[ "$(id -u)" != "0" ]]; then
      if sudo -n true >/dev/null 2>&1; then
        sudo dnf install -y "${packages[@]}"
        return 0
      fi
    elif [[ "$(id -u)" == "0" ]]; then
      dnf install -y "${packages[@]}"
      return 0
    fi
  fi

  if have yum; then
    if have sudo && [[ "$(id -u)" != "0" ]]; then
      if sudo -n true >/dev/null 2>&1; then
        sudo yum install -y "${packages[@]}"
        return 0
      fi
    elif [[ "$(id -u)" == "0" ]]; then
      yum install -y "${packages[@]}"
      return 0
    fi
  fi

  if have pacman; then
    if have sudo && [[ "$(id -u)" != "0" ]]; then
      if sudo -n true >/dev/null 2>&1; then
        sudo pacman -Sy --noconfirm "${packages[@]}"
        return 0
      fi
    elif [[ "$(id -u)" == "0" ]]; then
      pacman -Sy --noconfirm "${packages[@]}"
      return 0
    fi
  fi

  if have zypper; then
    if have sudo && [[ "$(id -u)" != "0" ]]; then
      if sudo -n true >/dev/null 2>&1; then
        sudo zypper --non-interactive install "${packages[@]}"
        return 0
      fi
    elif [[ "$(id -u)" == "0" ]]; then
      zypper --non-interactive install "${packages[@]}"
      return 0
    fi
  fi

  if have apk; then
    if [[ "$(id -u)" == "0" ]]; then
      apk add "${packages[@]}"
      return 0
    fi
    if have sudo && sudo -n true >/dev/null 2>&1; then
      sudo apk add "${packages[@]}"
      return 0
    fi
  fi

  if have brew; then
    local brew_packages=()
    local pkg
    for pkg in "${packages[@]}"; do
      case "$pkg" in
        tar)
          brew_packages+=("gnu-tar")
          ;;
        coreutils|curl)
          brew_packages+=("$pkg")
          ;;
        *)
          brew_packages+=("$pkg")
          ;;
      esac
    done
    brew install "${brew_packages[@]}"
    resolve_tar_cmd || true
    resolve_sha256_cmd || true
    return 0
  fi

  warn "Automatic dependency installation was skipped because no non-interactive package manager was available."
  return 1
}

detect_os() {
  case "$(uname -s)" in
    Linux*) echo "linux" ;;
    Darwin*) echo "darwin" ;;
    MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
    *) echo "unsupported" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *) echo "unsupported" ;;
  esac
}

is_windows_shell() {
  [[ "${OS}" == "windows" ]]
}

path_contains() {
  case ":${PATH}:" in
    *":$1:"*) return 0 ;;
    *) return 1 ;;
  esac
}

json_value() {
  local key="$1"
  awk -v key="$key" '
    {
      line = $0
      if (index(line, "\"" key "\"") == 0) {
        next
      }
      sub(".*\"" key "\":[[:space:]]*\"", "", line)
      sub("\".*", "", line)
      print line
      exit
    }
  '
}

checksum_for() {
  local file="$1"
  local checksums="$2"
  awk -v target="$file" '
    {
      candidate = $2
      sub(/^\*/, "", candidate)
      if (candidate == target) {
        print $1
        exit
      }
    }
  ' "$checksums"
}

source_archive_kind() {
  if [[ -n "$VERSION" ]]; then
    printf '%s' "tags"
  else
    printf '%s' "heads"
  fi
}

source_archive_ref() {
  if [[ -n "$VERSION" ]]; then
    printf '%s' "$VERSION"
  else
    printf '%s' "main"
  fi
}

source_archive_ext() {
  if is_windows_shell; then
    printf '%s' ".zip"
  else
    printf '%s' ".tar.gz"
  fi
}

source_archive_url() {
  printf 'https://github.com/%s/%s/archive/refs/%s/%s%s' \
    "$OWNER" \
    "$REPO" \
    "$(source_archive_kind)" \
    "$(source_archive_ref)" \
    "$(source_archive_ext)"
}

single_source_root() {
  local dir="$1"
  local entry
  for entry in "$dir"/*; do
    if [[ -d "$entry" ]]; then
      printf '%s' "$entry"
      return 0
    fi
  done
  return 1
}

extract_source_archive() {
  local archive_file="$1"
  local extract_dir="$2"

  if is_windows_shell; then
    local archive_file_win extract_dir_win
    archive_file_win="$(to_windows_path "$archive_file")"
    extract_dir_win="$(to_windows_path "$extract_dir")"
    ps_eval "\$ProgressPreference='SilentlyContinue'; Expand-Archive -LiteralPath $(ps_quote "$archive_file_win") -DestinationPath $(ps_quote "$extract_dir_win") -Force"
    return 0
  fi

  [[ -n "$TAR_CMD" ]] || resolve_tar_cmd || true
  [[ -n "$TAR_CMD" ]] || die "Need tar or gtar to extract the source archive."
  "$TAR_CMD" -xzf "$archive_file" -C "$extract_dir"
}

validate_installed_binary() {
  local binary_path="$1"
  local installed_version

  installed_version="$("$binary_path" version 2>/dev/null | tr -d '\r')"
  [[ -n "$installed_version" ]] || die "Installed Linker did not return a version string."
  INSTALLED_VERSION="$installed_version"
  log "Verified Linker version: ${installed_version}"
}

install_from_release() {
  local install_dir="$1"
  local binary_name="$2"
  local binary_path="$3"
  local artifact_base="$4"
  local artifact_ext="$5"
  local api_url="$6"
  local tmp_dir checksums_file expected_checksum archive_file checksums_url artifact_url source_binary source_binary_win target_binary_win archive_dir archive_file_win archive_dir_win

  if [[ -z "$VERSION" ]]; then
    log "Resolving latest release..."
    VERSION="$(fetch_url "$api_url" | json_value tag_name | tr -d '\r')"
  fi

  [[ -n "$VERSION" ]] || {
    warn "Could not resolve the latest Linker release; falling back to a source build."
    return 1
  }

  checksums_url="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}/checksums.txt"
  artifact_url="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}/${artifact_base}${artifact_ext}"

  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"; trap - RETURN' RETURN

  checksums_file="${tmp_dir}/checksums.txt"
  log "Downloading ${VERSION}..."
  if ! download_to "$checksums_url" "$checksums_file"; then
    warn "Release checksums are unavailable; falling back to a source build."
    return 1
  fi

  expected_checksum="$(checksum_for "${artifact_base}${artifact_ext}" "$checksums_file" | tr -d '\r')"
  [[ -n "$expected_checksum" ]] || {
    warn "Missing checksum for ${artifact_base}${artifact_ext}; falling back to a source build."
    return 1
  }

  if is_windows_shell; then
    local archive_dir
    archive_file="${tmp_dir}/${artifact_base}${artifact_ext}"
    archive_dir="${tmp_dir}/out"
    mkdir -p "$archive_dir"

    if ! download_to "$artifact_url" "$archive_file"; then
      warn "Release artifact is unavailable; falling back to a source build."
      return 1
    fi

    verify_sha256 "$archive_file" "$expected_checksum"

    archive_file_win="$(to_windows_path "$archive_file")"
    archive_dir_win="$(to_windows_path "$archive_dir")"
    ps_eval "\$ProgressPreference='SilentlyContinue'; Expand-Archive -LiteralPath $(ps_quote "$archive_file_win") -DestinationPath $(ps_quote "$archive_dir_win") -Force"

    source_binary="${archive_dir}/linker.exe"
    [[ -f "$source_binary" ]] || source_binary="${archive_dir}/linker"
    [[ -f "$source_binary" ]] || die "Windows archive did not contain linker.exe"

    mkdir -p "$install_dir"
    source_binary_win="$(to_windows_path "$source_binary")"
    target_binary_win="$(to_windows_path "$binary_path")"
    ps_eval "\$ProgressPreference='SilentlyContinue'; Copy-Item -LiteralPath $(ps_quote "$source_binary_win") -Destination $(ps_quote "$target_binary_win") -Force"
  else
    archive_file="${tmp_dir}/${artifact_base}${artifact_ext}"
    if ! download_to "$artifact_url" "$archive_file"; then
      warn "Release artifact is unavailable; falling back to a source build."
      return 1
    fi

    verify_sha256 "$archive_file" "$expected_checksum"

    mkdir -p "$install_dir"
    [[ -n "$TAR_CMD" ]] || resolve_tar_cmd || true
    [[ -n "$TAR_CMD" ]] || die "Need tar or gtar to extract the release archive."
    "$TAR_CMD" -xzf "$archive_file" -C "$tmp_dir"

    source_binary="${tmp_dir}/linker"
    [[ -f "$source_binary" ]] || die "Release archive did not contain linker"
    cp -f "$source_binary" "$binary_path"
    chmod 0755 "$binary_path"
  fi

  return 0
}

install_from_source() {
  local install_dir="$1"
  local binary_path="$2"
  local binary_name="$3"
  local tmp_dir source_url source_dir source_root source_archive

  log "Building Linker from source..."
  install_go_toolchain || die "Go is required to build Linker from source."
  have go || die "Go installation did not complete successfully."

  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"; trap - RETURN' RETURN

  source_url="$(source_archive_url)"
  source_dir="${tmp_dir}/source"
  source_archive="${tmp_dir}/source-archive$(source_archive_ext)"
  mkdir -p "$source_dir" "$install_dir"

  log "Downloading source archive..."
  download_to "$source_url" "$source_archive"
  extract_source_archive "$source_archive" "$source_dir"

  source_root="$(single_source_root "$source_dir")" || die "Source archive did not contain a repository root."

  (
    cd "$source_root"
    GO111MODULE=on go build -o "$binary_path" ./cmd/linker
  )

  [[ -f "$binary_path" ]] || die "Source build did not produce ${binary_name}."
  chmod 0755 "$binary_path"
}

ps_quote() {
  printf "'%s'" "$(printf '%s' "$1" | sed "s/'/''/g")"
}

to_windows_path() {
  if ! have cygpath; then
    die "cygpath is required on Windows shells (Git Bash/MSYS/Cygwin)."
  fi
  cygpath -aw "$1"
}

ps_eval() {
  powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -Command "$1"
}

fetch_url() {
  local url="$1"
  if have curl; then
    curl -fsSL "$url"
    return 0
  fi

  if have wget; then
    wget -qO- "$url"
    return 0
  fi

  if is_windows_shell; then
    ps_eval "\$ProgressPreference='SilentlyContinue'; (Invoke-WebRequest -UseBasicParsing -Uri $(ps_quote "$url")).Content"
    return 0
  fi

  die "curl is required on this platform."
}

download_to() {
  local url="$1"
  local out="$2"
  if have curl; then
    curl -fsSL "$url" -o "$out"
    return 0
  fi

  if have wget; then
    wget -qO "$out" "$url"
    return 0
  fi

  if is_windows_shell; then
    local out_win
    out_win="$(to_windows_path "$out")"
    ps_eval "\$ProgressPreference='SilentlyContinue'; Invoke-WebRequest -UseBasicParsing -Uri $(ps_quote "$url") -OutFile $(ps_quote "$out_win")"
    return 0
  fi

  die "curl is required on this platform."
}

verify_sha256() {
  local file="$1"
  local expected="$2"

  expected="$(printf '%s' "$expected" | tr -d '\r' | tr 'A-F' 'a-f')"

  if is_windows_shell; then
    local actual
    actual="$(
      ps_eval "\$ProgressPreference='SilentlyContinue'; (Get-FileHash -Algorithm SHA256 -LiteralPath $(ps_quote "$(to_windows_path "$file")")).Hash.ToLowerInvariant()"
    )"
    actual="$(printf '%s' "$actual" | tr -d '\r\n' | tr 'A-F' 'a-f')"
    [[ "$actual" == "$expected" ]] || die "Checksum mismatch for $(basename "$file")"
    return 0
  fi

  [[ -n "$SHA256_CMD" ]] || resolve_sha256_cmd || true
  [[ -n "$SHA256_CMD" ]] || die "Need sha256sum, gsha256sum, or shasum to verify the release checksum."

  printf '%s  %s\n' "$expected" "$file" | "$SHA256_CMD" "${SHA256_ARGS[@]}" -c -
}

choose_install_dir() {
  if [[ -n "$BIN_DIR_OVERRIDE" ]]; then
    printf '%s' "$BIN_DIR_OVERRIDE"
    return 0
  fi

  if is_windows_shell; then
    printf '%s' "${HOME:-}/.local/bin"
    return 0
  fi

  if [[ "$(id -u)" == "0" ]] || [[ -w /usr/local/bin ]]; then
    printf '%s' "/usr/local/bin"
    return 0
  fi

  printf '%s' "${HOME:-}/.local/bin"
}

require_platform_deps() {
  have uname || die "uname is required."
  have sed || die "sed is required."
  have awk || die "awk is required."
  have mktemp || die "mktemp is required."

  if is_windows_shell; then
    have powershell.exe || die "powershell.exe is required on Windows."
    have cygpath || die "cygpath is required on Windows shells (Git Bash/MSYS/Cygwin)."
    return 0
  fi

  if ! need_any_downloader && ! is_windows_shell; then
    install_missing_unix_tools || true
  fi

  if ! resolve_tar_cmd && ! is_windows_shell; then
    install_missing_unix_tools || true
  fi

  if ! resolve_sha256_cmd && ! is_windows_shell; then
    install_missing_unix_tools || true
  fi

  if ! need_any_downloader && ! is_windows_shell; then
    die "Need curl or wget to download the release."
  fi

  if ! resolve_tar_cmd && ! is_windows_shell; then
    die "Need tar or gtar to extract the release archive."
  fi

  if ! resolve_sha256_cmd && ! is_windows_shell; then
    warn "Checksum verification tools are unavailable; source build fallback will be used if needed."
  fi
}

main() {
  OS="$(detect_os)"
  ARCH="$(detect_arch)"

  [[ "$OS" != "unsupported" ]] || die "Unsupported operating system: $(uname -s)"
  [[ "$ARCH" != "unsupported" ]] || die "Unsupported CPU architecture: $(uname -m)"

  require_platform_deps

  local install_dir binary_name binary_path artifact_base artifact_ext api_url path_was_present

  install_dir="$(choose_install_dir)" || die "No writable install directory found. Set LINKER_BIN_DIR to a writable path, such as ~/.local/bin."

  binary_name="linker"
  artifact_base="linker_${OS}_${ARCH}"
  artifact_ext=".tar.gz"

  if is_windows_shell; then
    binary_name="linker.exe"
    artifact_ext=".zip"
  fi

  binary_path="${install_dir}/${binary_name}"
  api_url="https://api.github.com/repos/${OWNER}/${REPO}/releases/latest"

  log "Detected ${OS}/${ARCH}"
  log "Using install directory: ${install_dir}"

  if is_windows_shell || resolve_sha256_cmd; then
    if ! install_from_release "$install_dir" "$binary_name" "$binary_path" "$artifact_base" "$artifact_ext" "$api_url"; then
      install_from_source "$install_dir" "$binary_path" "$binary_name"
    fi
  else
    warn "Skipping release install because checksum verification tools are unavailable."
    install_from_source "$install_dir" "$binary_path" "$binary_name"
  fi

  validate_installed_binary "$binary_path"

  if path_contains "$install_dir"; then
    path_was_present=1
  else
    path_was_present=0
  fi

  export PATH="${install_dir}:${PATH}"

  log "Installed Linker ${INSTALLED_VERSION} to ${binary_path}"

  if [[ "$path_was_present" -eq 0 ]]; then
    if is_windows_shell; then
      log "PATH hint for Windows:"
      log "  Add this folder to your User PATH: $(to_windows_path "$install_dir")"
      log "  PowerShell example:"
      log "    [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';$(to_windows_path "$install_dir")', 'User')"
    else
      log "PATH hint:"
      log "  Add this to your shell profile:"
      log "    export PATH=\"${install_dir}:\$PATH\""
    fi
  fi

  log "Running linker onboard..."
  "$binary_path" onboard
}

main "$@"
