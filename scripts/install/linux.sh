#!/usr/bin/env bash
# CLIProxyAPI Codex 5h Auto-Ping Linux Plugin Installer
# Downloads, verifies, and installs cliproxyapi-auto-ping.so into CLIProxyAPI.

set -euo pipefail

REPO_OWNER="HungNth"
REPO_NAME="cliproxyapi-auto-ping"
PLUGIN_ID="cliproxyapi-auto-ping"
HOST_ROOT="${HOME}/cliproxyapi"
API_URL="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest"

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() {
  echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
  echo -e "${GREEN}[SUCCESS]${NC} $1"
}


log_error() {
  echo -e "${RED}[ERROR]${NC} $1" >&2
}

# 1. Platform & OS checks
check_os() {
  if [ "$(uname -s)" != "Linux" ]; then
    log_error "This script only supports Linux. Detected: $(uname -s)"
    exit 1
  fi
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)
      echo "amd64"
      ;;
    aarch64|arm64)
      echo "arm64"
      ;;
    *)
      log_error "Unsupported CPU architecture: $(uname -m). Only amd64 and arm64 are supported."
      exit 1
      ;;
  esac
}

check_libc() {
  if [ -f /etc/openwrt_release ]; then
    log_error "OpenWrt is not supported: CLIProxyAPI dynamic plugins are disabled on musl/OpenWrt."
    exit 1
  fi

  if [ -f /etc/os-release ] && grep -qi 'openwrt' /etc/os-release; then
    log_error "OpenWrt is not supported: CLIProxyAPI dynamic plugins are disabled on musl/OpenWrt."
    exit 1
  fi

  if [ -f /etc/alpine-release ]; then
    log_error "Alpine Linux (musl libc) is not supported. Plugin binaries require glibc."
    exit 1
  fi

  if command -v ldd >/dev/null 2>&1 && ldd --version 2>&1 | grep -qi 'musl'; then
    log_error "musl libc detected. Plugin binaries require glibc."
    exit 1
  fi

  for loader in /lib/ld-musl-*.so* /usr/lib/ld-musl-*.so*; do
    if [ -e "${loader}" ]; then
      log_error "musl dynamic loader detected (${loader}). Plugin binaries require glibc."
      exit 1
    fi
  done
}

# 2. Host precondition
check_host_root() {
  if [ ! -d "${HOST_ROOT}" ]; then
    log_error "CLIProxyAPI installation directory not found at ${HOST_ROOT}."
    log_error "Please install CLIProxyAPI first before installing plugins."
    exit 1
  fi
}

# 3. Tool dependencies
check_tools() {
  local missing=()

  if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
    missing+=("curl or wget")
  fi

  if ! command -v unzip >/dev/null 2>&1; then
    missing+=("unzip")
  fi

  if ! command -v sha256sum >/dev/null 2>&1; then
    missing+=("sha256sum")
  fi

  if [ ${#missing[@]} -gt 0 ]; then
    log_error "Missing required tools: ${missing[*]}"
    log_info "On Ubuntu/Debian: sudo apt-get update && sudo apt-get install -y curl unzip coreutils"
    log_info "On CentOS/RHEL/Fedora: sudo dnf install -y curl unzip coreutils"
    exit 1
  fi
}

download_to() {
  local url="$1"
  local output="$2"

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "${output}" "${url}"
  else
    wget -q -O "${output}" "${url}"
  fi
}

fetch_text() {
  local url="$1"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "${url}"
  else
    wget -qO- "${url}"
  fi
}

main() {
  check_os
  local arch
  arch="$(detect_arch)"
  check_libc
  check_host_root
  check_tools

  local target_dir="${HOST_ROOT}/plugins/linux/${arch}"
  local target_file="${target_dir}/${PLUGIN_ID}.so"

  log_info "Fetching latest release information for ${REPO_OWNER}/${REPO_NAME}..."
  local release_json
  release_json="$(fetch_text "${API_URL}")"

  local version
  version="$(echo "${release_json}" | grep -o '"tag_name": *"[^"]*"' | head -n 1 | cut -d'"' -f4 | sed 's/^v//')"

  if [ -z "${version}" ]; then
    log_error "Failed to determine latest release version from GitHub."
    exit 1
  fi

  log_info "Detected latest version: v${version}"
  local asset_name="${PLUGIN_ID}_${version}_linux_${arch}.zip"
  local checksums_name="checksums.txt"

  local asset_url=""
  local checksums_url=""

  asset_url="$(echo "${release_json}" | grep -o '"browser_download_url": *"[^"]*"' | cut -d'"' -f4 | grep "/${asset_name}$" | head -n 1 || true)"
  checksums_url="$(echo "${release_json}" | grep -o '"browser_download_url": *"[^"]*"' | cut -d'"' -f4 | grep "/${checksums_name}$" | head -n 1 || true)"

  if [ -z "${asset_url}" ]; then
    log_error "Release asset not found: ${asset_name}"
    exit 1
  fi

  if [ -z "${checksums_url}" ]; then
    log_error "Checksums asset not found: ${checksums_name}"
    exit 1
  fi

  WORK_DIR="$(mktemp -d)"
  trap 'if [ -n "${WORK_DIR:-}" ] && [ -d "${WORK_DIR:-}" ]; then rm -rf "${WORK_DIR}"; fi' EXIT
  local zip_file="${WORK_DIR}/${asset_name}"
  local checksums_file="${WORK_DIR}/${checksums_name}"


  log_info "Downloading ${asset_name}..."
  download_to "${asset_url}" "${zip_file}"

  log_info "Downloading ${checksums_name}..."
  download_to "${checksums_url}" "${checksums_file}"

  log_info "Verifying SHA-256 checksum..."
  local expected_hash
  expected_hash="$(awk -v asset="${asset_name}" 'NF == 2 && ($2 == asset || $2 == "*" asset) { print $1 }' "${checksums_file}")"
  local line_count
  line_count="$(printf "%s\n" "${expected_hash}" | grep -v '^$' | wc -l || true)"
  if [ "${line_count}" -ne 1 ] || ! echo "${expected_hash}" | grep -Eq '^[0-9a-fA-F]{64}$'; then
    log_error "Exact 64-hex SHA-256 checksum for ${asset_name} not found or malformed in ${checksums_name}."
    exit 1
  fi

  (cd "${WORK_DIR}" && printf "%s  %s\n" "${expected_hash}" "${asset_name}" | sha256sum -c --status) || {
    log_error "SHA-256 checksum verification failed for ${asset_name}!"
    exit 1
  }

  log_success "Checksum verified: ${expected_hash}"

  mkdir -p "${target_dir}"

  local temp_so
  temp_so="$(mktemp "${target_dir}/.tmp-plugin-XXXXXX")"

  log_info "Extracting ${PLUGIN_ID}.so from archive..."
  if ! unzip -p "${zip_file}" "${PLUGIN_ID}.so" > "${temp_so}"; then
    rm -f "${temp_so}"
    log_error "Failed to extract ${PLUGIN_ID}.so from archive."
    exit 1
  fi

  if [ ! -s "${temp_so}" ]; then
    rm -f "${temp_so}"
    log_error "Extracted ${PLUGIN_ID}.so is empty."
    exit 1
  fi

  chmod 755 "${temp_so}"
  mv -f "${temp_so}" "${target_file}"

  log_success "Installed successfully to: ${target_file}"
  echo
  log_info "Next steps:"
  log_info "1. Configure ${PLUGIN_ID} in ${HOST_ROOT}/config.yaml (if not already configured)."
  log_info "2. Restart or reload CLIProxyAPI to load the updated plugin."
}

main "$@"
