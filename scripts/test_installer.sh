#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALLER="${ROOT_DIR}/scripts/install/linux.sh"

run_test() {
  local name="$1"
  local fn="$2"
  echo "--- Running test: ${name} ---"
  local tmp
  tmp="$(mktemp -d)"
  local status=0
  (
    "${fn}" "${tmp}"
  ) || status=$?
  rm -rf "${tmp}"
  if [ "${status}" -ne 0 ]; then
    echo "FAIL: ${name}" >&2
    exit "${status}"
  fi
  echo "PASS: ${name}"
}

setup_isolated_env() {
  local tmp="$1"
  mkdir -p "${tmp}/system_bin"
  for cmd in bash sh cat grep awk sed mktemp chmod mv rm dirname printf echo tr cut head test [; do
    local real_cmd
    real_cmd="$(which "$cmd")"
    ln -s "${real_cmd}" "${tmp}/system_bin/${cmd}"
  done
}

get_release_json() {
  cat << 'EOF'
{
  "tag_name": "v0.2.2",
  "assets": [
    {
      "name": "cliproxyapi-auto-ping_0.2.2_linux_amd64.zip",
      "browser_download_url": "https://example.com/cliproxyapi-auto-ping_0.2.2_linux_amd64.zip"
    },
    {
      "name": "cliproxyapi-auto-ping_0.2.2_linux_arm64.zip",
      "browser_download_url": "https://example.com/cliproxyapi-auto-ping_0.2.2_linux_arm64.zip"
    },
    {
      "name": "checksums.txt",
      "browser_download_url": "https://example.com/checksums.txt"
    }
  ]
}
EOF
}

create_mock_uname() {
  local dir="$1"
  local machine="$2"
  cat << EOF > "${dir}/uname"
#!/bin/sh
if [ "\$1" = "-m" ]; then
  echo "${machine}"
elif [ "\$1" = "-s" ]; then
  echo "Linux"
else
  echo "Linux"
fi
EOF
  chmod +x "${dir}/uname"
}

create_mock_curl() {
  local dir="$1"
  local release_json="$2"
  local checksums_txt="$3"
  local zip_content="$4"
  local expected_zip_asset="$5"

  cat << 'EOF' > "${dir}/curl"
#!/bin/sh
set -eu
output=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o)
      output="$2"
      shift 2
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done

if [ -n "${MOCK_CANARY:-}" ]; then
  echo "called" > "${MOCK_CANARY}"
fi

if echo "$url" | grep -q "releases/latest"; then
  cat << 'JSON'
EOF
  cat << EOF >> "${dir}/curl"
${release_json}
JSON
elif echo "\$url" | grep -q "checksums.txt"; then
  cat << 'CHECKSUMS' > "\$output"
${checksums_txt}
CHECKSUMS
elif echo "\$url" | grep -q "\.zip$"; then
  case "\$url" in
    *"/${expected_zip_asset}") ;;
    *)
      echo "Downloaded unexpected zip asset URL: \$url (expected ${expected_zip_asset})" >&2
      exit 1
      ;;
  esac
  printf "%s" "${zip_content}" > "\$output"
else
  echo "unexpected curl url: \$url" >&2
  exit 1
fi
EOF
  chmod +x "${dir}/curl"
}

# Scenario 1: amd64 successful fresh install with real sha256sum verification and exact asset check
test_amd64_success() {
  local tmp="$1"

  mkdir -p "${tmp}/home/cliproxyapi"
  mkdir -p "${tmp}/bin"
  create_mock_uname "${tmp}/bin" "x86_64"

  local release_json
  release_json="$(get_release_json)"
  local checksums_txt='e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  cliproxyapi-auto-ping_0.2.2_linux_arm64.zip
262c6dba1d4c95fe436b39e3e1e2a6e98477e6d97eb8e8b2243b50e119e34dd9  cliproxyapi-auto-ping_0.2.2_linux_amd64.zip'

  create_mock_curl "${tmp}/bin" "${release_json}" "${checksums_txt}" "dummy-zip-content" "cliproxyapi-auto-ping_0.2.2_linux_amd64.zip"
  ln -s "$(which sha256sum)" "${tmp}/bin/sha256sum"

  cat << 'EOF' > "${tmp}/bin/unzip"
#!/bin/sh
set -eu
if [ "$1" = "-p" ]; then
  printf "binary-plugin-bytes"
else
  echo "unexpected unzip flags: $*" >&2
  exit 1
fi
EOF
  chmod +x "${tmp}/bin/unzip"

  local output
  output="$(PATH="${tmp}/bin:/usr/bin:/bin" HOME="${tmp}/home" bash "${INSTALLER}")"

  local target="${tmp}/home/cliproxyapi/plugins/linux/amd64/cliproxyapi-auto-ping.so"
  if [ ! -f "${target}" ]; then
    echo "Expected ${target} to exist!" >&2
    exit 1
  fi
  local content
  content="$(cat "${target}")"
  if [ "${content}" != "binary-plugin-bytes" ]; then
    echo "Unexpected target content: ${content}" >&2
    exit 1
  fi

  if ! echo "${output}" | grep -q "Downloading cliproxyapi-auto-ping_0.2.2_linux_amd64.zip"; then
    echo "Output missing download confirmation for amd64: ${output}" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -q "Installed successfully to: ${target}"; then
    echo "Output missing expected success destination: ${output}" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -qi "restart or reload"; then
    echo "Output missing restart/reload instruction: ${output}" >&2
    exit 1
  fi
}

# Scenario 2: upgrade over existing plugin replaces bytes atomically and preserves target directory
test_upgrade_existing_plugin() {
  local tmp="$1"

  local target_dir="${tmp}/home/cliproxyapi/plugins/linux/amd64"
  mkdir -p "${target_dir}"
  echo "old-version-plugin-bytes" > "${target_dir}/cliproxyapi-auto-ping.so"
  mkdir -p "${tmp}/bin"
  create_mock_uname "${tmp}/bin" "x86_64"

  local release_json
  release_json="$(get_release_json)"
  local checksums_txt='262c6dba1d4c95fe436b39e3e1e2a6e98477e6d97eb8e8b2243b50e119e34dd9  cliproxyapi-auto-ping_0.2.2_linux_amd64.zip'

  create_mock_curl "${tmp}/bin" "${release_json}" "${checksums_txt}" "dummy-zip-content" "cliproxyapi-auto-ping_0.2.2_linux_amd64.zip"
  ln -s "$(which sha256sum)" "${tmp}/bin/sha256sum"

  cat << 'EOF' > "${tmp}/bin/unzip"
#!/bin/sh
printf "upgraded-plugin-bytes"
EOF
  chmod +x "${tmp}/bin/unzip"

  local output
  output="$(PATH="${tmp}/bin:/usr/bin:/bin" HOME="${tmp}/home" bash "${INSTALLER}")"

  local target="${target_dir}/cliproxyapi-auto-ping.so"
  local content
  content="$(cat "${target}")"
  if [ "${content}" != "upgraded-plugin-bytes" ]; then
    echo "Plugin was not upgraded! Content: ${content}" >&2
    exit 1
  fi

  if ! echo "${output}" | grep -q "Installed successfully to: ${target}"; then
    echo "Output missing expected success destination: ${output}" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -qi "restart or reload"; then
    echo "Output missing restart/reload instruction: ${output}" >&2
    exit 1
  fi
}

# Scenario 3: arm64 mapping (aarch64 -> arm64) with real sha256sum and exact arm64 asset check
test_arm64_mapping() {
  local tmp="$1"

  mkdir -p "${tmp}/home/cliproxyapi"
  mkdir -p "${tmp}/bin"
  create_mock_uname "${tmp}/bin" "aarch64"

  local release_json
  release_json="$(get_release_json)"
  local checksums_txt='d00392c04bfb1ddd2d79e07a689b697cd813422f7a726334c35ffe923ccd7aac  cliproxyapi-auto-ping_0.2.2_linux_arm64.zip'

  create_mock_curl "${tmp}/bin" "${release_json}" "${checksums_txt}" "arm64-plugin-bytes" "cliproxyapi-auto-ping_0.2.2_linux_arm64.zip"
  ln -s "$(which sha256sum)" "${tmp}/bin/sha256sum"

  cat << 'EOF' > "${tmp}/bin/unzip"
#!/bin/sh
printf "arm64-plugin-bytes"
EOF
  chmod +x "${tmp}/bin/unzip"

  local output
  output="$(PATH="${tmp}/bin:/usr/bin:/bin" HOME="${tmp}/home" bash "${INSTALLER}")"

  local target="${tmp}/home/cliproxyapi/plugins/linux/arm64/cliproxyapi-auto-ping.so"
  if [ ! -f "${target}" ]; then
    echo "Expected ${target} to exist!" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -q "Downloading cliproxyapi-auto-ping_0.2.2_linux_arm64.zip"; then
    echo "Output missing download confirmation for arm64: ${output}" >&2
    exit 1
  fi
}

# Scenario 4: checksum mismatch preserves existing plugin and exits non-zero (tested with real sha256sum)
test_checksum_mismatch() {
  local tmp="$1"

  local target_dir="${tmp}/home/cliproxyapi/plugins/linux/amd64"
  mkdir -p "${target_dir}"
  echo "pre-existing-content" > "${target_dir}/cliproxyapi-auto-ping.so"
  mkdir -p "${tmp}/bin"
  create_mock_uname "${tmp}/bin" "x86_64"

  local release_json
  release_json="$(get_release_json)"
  local checksums_txt='0000000000000000000000000000000000000000000000000000000000000000  cliproxyapi-auto-ping_0.2.2_linux_amd64.zip'

  create_mock_curl "${tmp}/bin" "${release_json}" "${checksums_txt}" "corrupted-content" "cliproxyapi-auto-ping_0.2.2_linux_amd64.zip"
  ln -s "$(which sha256sum)" "${tmp}/bin/sha256sum"

  cat << 'EOF' > "${tmp}/bin/unzip"
#!/bin/sh
echo "unzip should not be called!" >&2
exit 1
EOF
  chmod +x "${tmp}/bin/unzip"

  set +e
  local output
  output="$(PATH="${tmp}/bin:/usr/bin:/bin" HOME="${tmp}/home" bash "${INSTALLER}" 2>&1)"
  local exit_code=$?
  set -e

  if [ "${exit_code}" -eq 0 ]; then
    echo "Expected non-zero exit code on checksum failure!" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -q "SHA-256 checksum verification failed"; then
    echo "Missing clear checksum failure message: ${output}" >&2
    exit 1
  fi

  local content
  content="$(cat "${target_dir}/cliproxyapi-auto-ping.so")"
  if [ "${content}" != "pre-existing-content" ]; then
    echo "Existing plugin was modified on checksum failure!" >&2
    exit 1
  fi
}

# Scenario 5: missing checksum entry fails closed and preserves existing plugin
test_missing_checksum() {
  local tmp="$1"

  local target_dir="${tmp}/home/cliproxyapi/plugins/linux/amd64"
  mkdir -p "${target_dir}"
  echo "pre-existing-content" > "${target_dir}/cliproxyapi-auto-ping.so"
  mkdir -p "${tmp}/bin"
  create_mock_uname "${tmp}/bin" "x86_64"

  local release_json
  release_json="$(get_release_json)"
  local checksums_txt='other-asset-hash  different-asset.zip'

  create_mock_curl "${tmp}/bin" "${release_json}" "${checksums_txt}" "some-bytes" "cliproxyapi-auto-ping_0.2.2_linux_amd64.zip"
  ln -s "$(which sha256sum)" "${tmp}/bin/sha256sum"

  cat << 'EOF' > "${tmp}/bin/unzip"
#!/bin/sh
echo "unzip should not be called!" >&2
exit 1
EOF
  chmod +x "${tmp}/bin/unzip"

  set +e
  local output
  output="$(PATH="${tmp}/bin:/usr/bin:/bin" HOME="${tmp}/home" bash "${INSTALLER}" 2>&1)"
  local exit_code=$?
  set -e

  if [ "${exit_code}" -eq 0 ]; then
    echo "Expected non-zero exit code when checksum entry missing!" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -q "Exact 64-hex SHA-256 checksum for cliproxyapi-auto-ping_0.2.2_linux_amd64.zip not found or malformed"; then
    echo "Missing clear checksum failure message: ${output}" >&2
    exit 1
  fi

  local content
  content="$(cat "${target_dir}/cliproxyapi-auto-ping.so")"
  if [ "${content}" != "pre-existing-content" ]; then
    echo "Existing plugin was modified when checksum entry missing!" >&2
    exit 1
  fi
}

# Scenario 6: malformed or multiple checksum entries fail closed
test_malformed_checksum_entries() {
  local tmp="$1"

  local target_dir="${tmp}/home/cliproxyapi/plugins/linux/amd64"
  mkdir -p "${target_dir}"
  echo "pre-existing-content" > "${target_dir}/cliproxyapi-auto-ping.so"
  mkdir -p "${tmp}/bin"
  create_mock_uname "${tmp}/bin" "x86_64"

  local release_json
  release_json="$(get_release_json)"
  # Line with unexpected extra field
  local checksums_txt='262c6dba1d4c95fe436b39e3e1e2a6e98477e6d97eb8e8b2243b50e119e34dd9  cliproxyapi-auto-ping_0.2.2_linux_amd64.zip  unexpected-extra-field'

  create_mock_curl "${tmp}/bin" "${release_json}" "${checksums_txt}" "dummy-zip-content" "cliproxyapi-auto-ping_0.2.2_linux_amd64.zip"
  ln -s "$(which sha256sum)" "${tmp}/bin/sha256sum"

  cat << 'EOF' > "${tmp}/bin/unzip"
#!/bin/sh
exit 1
EOF
  chmod +x "${tmp}/bin/unzip"

  set +e
  local output
  output="$(PATH="${tmp}/bin:/usr/bin:/bin" HOME="${tmp}/home" bash "${INSTALLER}" 2>&1)"
  local exit_code=$?
  set -e

  if [ "${exit_code}" -eq 0 ]; then
    echo "Expected non-zero exit code on malformed checksum entry!" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -q "Exact 64-hex SHA-256 checksum for cliproxyapi-auto-ping_0.2.2_linux_amd64.zip not found or malformed"; then
    echo "Missing clear checksum failure message: ${output}" >&2
    exit 1
  fi

  local content
  content="$(cat "${target_dir}/cliproxyapi-auto-ping.so")"
  if [ "${content}" != "pre-existing-content" ]; then
    echo "Existing plugin was modified on malformed checksum entries!" >&2
    exit 1
  fi
}

# Scenario 7: missing host root fails before download and proves no downloader ran
test_missing_host_root() {
  local tmp="$1"

  mkdir -p "${tmp}/home"
  mkdir -p "${tmp}/bin"
  create_mock_uname "${tmp}/bin" "x86_64"

  cat << EOF > "${tmp}/bin/curl"
#!/bin/sh
echo "CURL_CALLED" > "${tmp}/curl_called"
exit 1
EOF
  chmod +x "${tmp}/bin/curl"

  set +e
  local output
  output="$(PATH="${tmp}/bin:/usr/bin:/bin" HOME="${tmp}/home" bash "${INSTALLER}" 2>&1)"
  local exit_code=$?
  set -e

  if [ "${exit_code}" -eq 0 ]; then
    echo "Expected non-zero exit code when host root is missing!" >&2
    exit 1
  fi
  if [ -f "${tmp}/curl_called" ]; then
    echo "Downloader was executed despite missing host root!" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -q "CLIProxyAPI installation directory not found at"; then
    echo "Missing clear host root error message: ${output}" >&2
    exit 1
  fi
  if [ -d "${tmp}/home/cliproxyapi" ]; then
    echo "Installer created orphaned directory when host root was absent!" >&2
    exit 1
  fi
}

# Scenario 8: musl / OpenWrt rejection proves no downloader ran
test_musl_rejection() {
  local tmp="$1"

  mkdir -p "${tmp}/home/cliproxyapi"
  mkdir -p "${tmp}/bin"
  create_mock_uname "${tmp}/bin" "x86_64"

  cat << 'EOF' > "${tmp}/bin/ldd"
#!/bin/sh
echo "musl libc (x86_64)"
EOF
  chmod +x "${tmp}/bin/ldd"

  cat << EOF > "${tmp}/bin/curl"
#!/bin/sh
echo "CURL_CALLED" > "${tmp}/curl_called"
exit 1
EOF
  chmod +x "${tmp}/bin/curl"

  set +e
  local output
  output="$(PATH="${tmp}/bin:/usr/bin:/bin" HOME="${tmp}/home" bash "${INSTALLER}" 2>&1)"
  local exit_code=$?
  set -e

  if [ "${exit_code}" -eq 0 ]; then
    echo "Expected non-zero exit code on musl system!" >&2
    exit 1
  fi
  if [ -f "${tmp}/curl_called" ]; then
    echo "Downloader was executed despite musl libc!" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -q "musl libc detected. Plugin binaries require glibc."; then
    echo "Missing clear musl rejection error message: ${output}" >&2
    exit 1
  fi
}

# Scenario 9: unsupported CPU architecture rejection proves no downloader ran
test_unsupported_arch() {
  local tmp="$1"

  mkdir -p "${tmp}/home/cliproxyapi"
  mkdir -p "${tmp}/bin"
  create_mock_uname "${tmp}/bin" "riscv64"

  cat << EOF > "${tmp}/bin/curl"
#!/bin/sh
echo "CURL_CALLED" > "${tmp}/curl_called"
exit 1
EOF
  chmod +x "${tmp}/bin/curl"

  set +e
  local output
  output="$(PATH="${tmp}/bin:/usr/bin:/bin" HOME="${tmp}/home" bash "${INSTALLER}" 2>&1)"
  local exit_code=$?
  set -e

  if [ "${exit_code}" -eq 0 ]; then
    echo "Expected non-zero exit code on unsupported CPU architecture!" >&2
    exit 1
  fi
  if [ -f "${tmp}/curl_called" ]; then
    echo "Downloader was executed despite unsupported CPU architecture!" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -q "Unsupported CPU architecture: riscv64"; then
    echo "Missing clear unsupported architecture error message: ${output}" >&2
    exit 1
  fi
}

# Scenario 10: missing unzip dependency with strict isolated PATH proves clear error and no downloader ran
test_missing_unzip() {
  local tmp="$1"

  mkdir -p "${tmp}/home/cliproxyapi"
  mkdir -p "${tmp}/bin"
  setup_isolated_env "${tmp}"
  create_mock_uname "${tmp}/bin" "x86_64"

  cat << EOF > "${tmp}/bin/curl"
#!/bin/sh
echo "CURL_CALLED" > "${tmp}/curl_called"
exit 1
EOF
  chmod +x "${tmp}/bin/curl"
  ln -s "$(which sha256sum)" "${tmp}/bin/sha256sum"

  # Notice: unzip is completely omitted

  set +e
  local output
  output="$(PATH="${tmp}/bin:${tmp}/system_bin" HOME="${tmp}/home" bash "${INSTALLER}" 2>&1)"
  local exit_code=$?
  set -e

  if [ "${exit_code}" -eq 0 ]; then
    echo "Expected non-zero exit code when unzip is missing!" >&2
    exit 1
  fi
  if [ -f "${tmp}/curl_called" ]; then
    echo "Downloader was executed despite missing unzip tool!" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -q "Missing required tools: unzip"; then
    echo "Missing clear unzip dependency error message: ${output}" >&2
    exit 1
  fi
}

# Scenario 11: missing sha256sum dependency with strict isolated PATH proves clear error and no downloader ran
test_missing_sha256sum() {
  local tmp="$1"

  mkdir -p "${tmp}/home/cliproxyapi"
  mkdir -p "${tmp}/bin"
  setup_isolated_env "${tmp}"
  create_mock_uname "${tmp}/bin" "x86_64"

  cat << EOF > "${tmp}/bin/curl"
#!/bin/sh
echo "CURL_CALLED" > "${tmp}/curl_called"
exit 1
EOF
  chmod +x "${tmp}/bin/curl"
  ln -s "$(which unzip)" "${tmp}/bin/unzip"

  # Notice: sha256sum is completely omitted

  set +e
  local output
  output="$(PATH="${tmp}/bin:${tmp}/system_bin" HOME="${tmp}/home" bash "${INSTALLER}" 2>&1)"
  local exit_code=$?
  set -e

  if [ "${exit_code}" -eq 0 ]; then
    echo "Expected non-zero exit code when sha256sum is missing!" >&2
    exit 1
  fi
  if [ -f "${tmp}/curl_called" ]; then
    echo "Downloader was executed despite missing sha256sum tool!" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -q "Missing required tools: sha256sum"; then
    echo "Missing clear sha256sum dependency error message: ${output}" >&2
    exit 1
  fi
}

# Scenario 12: missing downloader (neither curl nor wget) with strict isolated PATH proves clear error
test_missing_downloader() {
  local tmp="$1"

  mkdir -p "${tmp}/home/cliproxyapi"
  mkdir -p "${tmp}/bin"
  setup_isolated_env "${tmp}"
  create_mock_uname "${tmp}/bin" "x86_64"

  ln -s "$(which sha256sum)" "${tmp}/bin/sha256sum"
  ln -s "$(which unzip)" "${tmp}/bin/unzip"

  # Notice: curl and wget are completely omitted

  set +e
  local output
  output="$(PATH="${tmp}/bin:${tmp}/system_bin" HOME="${tmp}/home" bash "${INSTALLER}" 2>&1)"
  local exit_code=$?
  set -e

  if [ "${exit_code}" -eq 0 ]; then
    echo "Expected non-zero exit code when both curl and wget are missing!" >&2
    exit 1
  fi
  if ! echo "${output}" | grep -q "Missing required tools: curl or wget"; then
    echo "Missing clear downloader dependency error message: ${output}" >&2
    exit 1
  fi
}

run_test "amd64 successful install with real sha256sum and exact asset check" test_amd64_success
run_test "upgrade existing plugin with real sha256sum" test_upgrade_existing_plugin
run_test "arm64 mapping with real sha256sum and exact asset check" test_arm64_mapping
run_test "checksum mismatch failure with real sha256sum" test_checksum_mismatch
run_test "missing checksum entry failure" test_missing_checksum
run_test "malformed or multiple checksum entries failure" test_malformed_checksum_entries
run_test "missing host root failure" test_missing_host_root
run_test "musl system rejection" test_musl_rejection
run_test "unsupported architecture rejection" test_unsupported_arch
run_test "missing unzip dependency" test_missing_unzip
run_test "missing sha256sum dependency" test_missing_sha256sum
run_test "missing downloader dependency" test_missing_downloader

echo "ALL HARDENED BLACK-BOX TESTS PASSED!"
