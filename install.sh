#!/usr/bin/env bash
set -euo pipefail

OWNER="${LINKER_OWNER:-linker-cli}"
REPO="${LINKER_REPO:-linker}"
BIN_DIR="${LINKER_BIN_DIR:-/usr/local/bin}"

is_wsl() {
  [[ -f /proc/version ]] && grep -qi microsoft /proc/version
}

detect_os() {
  case "$(uname -s)" in
    Linux*)
      echo "linux"
      ;;
    Darwin*)
      echo "darwin"
      ;;
    MINGW*|MSYS*|CYGWIN*)
      echo "windows"
      ;;
    *)
      echo "unsupported"
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)
      echo "amd64"
      ;;
    arm64|aarch64)
      echo "arm64"
      ;;
    *)
      echo "unsupported"
      ;;
  esac
}

sha256_check() {
  local file="$1"
  local expected="$2"
  if command -v sha256sum >/dev/null 2>&1; then
    echo "${expected}  ${file}" | sha256sum -c -
  else
    echo "${expected}  ${file}" | shasum -a 256 -c -
  fi
}

json_value() {
  local key="$1"
  sed -n "s/.*\"${key}\": *\"\\([^\"]*\\)\".*/\\1/p" | head -n 1
}

OS="$(detect_os)"
ARCH="$(detect_arch)"

if [[ "${OS}" == "unsupported" || "${ARCH}" == "unsupported" ]]; then
  echo "Unsupported platform: $(uname -s) $(uname -m)" >&2
  exit 1
fi

if [[ "${OS}" == "linux" && "$(is_wsl && echo yes || echo no)" == "yes" ]]; then
  OS="linux"
fi

VERSION="${LINKER_VERSION:-}"
if [[ -z "${VERSION}" ]]; then
  VERSION="$(
    curl -fsSL "https://api.github.com/repos/${OWNER}/${REPO}/releases/latest" \
      | json_value tag_name
  )"
fi

if [[ -z "${VERSION}" ]]; then
  echo "Could not resolve the latest Linker release." >&2
  exit 1
fi

ARTIFACT_BASENAME="linker_${OS}_${ARCH}"
TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

CHECKSUMS_URL="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}/checksums.txt"
curl -fsSL "${CHECKSUMS_URL}" -o "${TMP_DIR}/checksums.txt"

if [[ "${OS}" == "windows" ]]; then
  ARTIFACT="${ARTIFACT_BASENAME}.zip"
  URL="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}/${ARTIFACT}"
  curl -fsSL "${URL}" -o "${TMP_DIR}/${ARTIFACT}"
  EXPECTED="$(grep " ${ARTIFACT}$" "${TMP_DIR}/checksums.txt" | awk '{print $1}')"
  if [[ -z "${EXPECTED}" ]]; then
    echo "Missing checksum for ${ARTIFACT}" >&2
    exit 1
  fi
  sha256_check "${TMP_DIR}/${ARTIFACT}" "${EXPECTED}"
  unzip -q "${TMP_DIR}/${ARTIFACT}" -d "${TMP_DIR}/out"
  mkdir -p "${BIN_DIR}"
  install "${TMP_DIR}/out/linker.exe" "${BIN_DIR}/linker.exe"
  "${BIN_DIR}/linker.exe" onboard
else
  ARTIFACT="${ARTIFACT_BASENAME}.tar.gz"
  URL="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}/${ARTIFACT}"
  curl -fsSL "${URL}" -o "${TMP_DIR}/${ARTIFACT}"
  EXPECTED="$(grep " ${ARTIFACT}$" "${TMP_DIR}/checksums.txt" | awk '{print $1}')"
  if [[ -z "${EXPECTED}" ]]; then
    echo "Missing checksum for ${ARTIFACT}" >&2
    exit 1
  fi
  sha256_check "${TMP_DIR}/${ARTIFACT}" "${EXPECTED}"
  tar -xzf "${TMP_DIR}/${ARTIFACT}" -C "${TMP_DIR}"
  mkdir -p "${BIN_DIR}"
  install "${TMP_DIR}/linker" "${BIN_DIR}/linker"
  "${BIN_DIR}/linker" onboard
fi
