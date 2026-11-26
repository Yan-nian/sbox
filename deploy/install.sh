#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# sbox-panel Linux One-Click Installer / Upgrader / Uninstaller
# Supports Linux amd64 & arm64 with systemd, public & private GitHub Releases,
# and SHA-256 verification against checksums.txt.
# Preserves data directory across upgrades (and uninstalls unless --purge).
# ==============================================================================

BINARY_NAME="sbox-panel"
INSTALL_PATH="${SBOX_BIN_PATH:-/usr/local/bin/${BINARY_NAME}}"
SERVICE_BASE="${SBOX_SERVICE_NAME:-sbox-panel}"
DATA_DIR="${SBOX_DATA_DIR:-/var/lib/sbox-panel}"
PORT="${SBOX_PORT:-18080}"
LISTEN_ADDR="${SBOX_LISTEN:-}"
PORT_EXPLICIT=0
LISTEN_EXPLICIT=0

REPO="${SBOX_REPO:-Yan-nian/sbox}"
VERSION="${SBOX_VERSION:-latest}"
GH_TOKEN="${GITHUB_TOKEN:-}"
DOWNLOAD_URL="${SBOX_DOWNLOAD_URL:-}"
LOCAL_BINARY=""
GH_PROXY="${SBOX_GH_PROXY:-}"
SKIP_VERIFY=0
ACTION="install"
PURGE=0
TMP_DIR=""

SERVICE_NAME=""
SERVICE_PATH=""

if [ -t 1 ]; then
  C_RESET='\033[0m'
  C_INFO='\033[0;36m'
  C_OK='\033[0;32m'
  C_WARN='\033[0;33m'
  C_ERR='\033[0;31m'
else
  C_RESET=''
  C_INFO=''
  C_OK=''
  C_WARN=''
  C_ERR=''
fi

log_info() { printf "%b[INFO]%b %s\n" "$C_INFO" "$C_RESET" "$*"; }
log_ok()   { printf "%b[OK]%b   %s\n" "$C_OK" "$C_RESET" "$*"; }
log_warn() { printf "%b[WARN]%b %s\n" "$C_WARN" "$C_RESET" "$*" >&2; }
log_err()  { printf "%b[ERR]%b  %s\n" "$C_ERR" "$C_RESET" "$*" >&2; }

cleanup() {
  if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

usage() {
  cat <<EOF
sbox-panel Linux installer

Usage:
  sudo bash install.sh [install|upgrade|uninstall] [options]

Commands:
  install                  Install sbox-panel (or upgrade in-place if already installed) [default]
  upgrade                  Upgrade binary and refresh systemd unit while preserving data
  uninstall                Stop/remove service and binary (preserves data unless --purge)

Options:
  --repo <owner/repo>      GitHub repository (env: SBOX_REPO, default: ${REPO})
  --version <tag|latest>   Release tag to install (env: SBOX_VERSION, default: ${VERSION})
  --token <gh_token>       GitHub token for private repos / higher API limits (env: GITHUB_TOKEN)
  --service-name <name>    systemd service name without .service (env: SBOX_SERVICE_NAME, default: ${SERVICE_BASE})
  --data-dir <dir>         Persistent data directory (env: SBOX_DATA_DIR, default: ${DATA_DIR})
  --port <port>            Panel listen port (env: SBOX_PORT, default: ${PORT})
  --listen <addr>          Explicit listen address host:port (env: SBOX_LISTEN, overrides --port)
  --bin-path <path>        Target binary install path (env: SBOX_BIN_PATH, default: ${INSTALL_PATH})
  --binary <path>          Install/upgrade directly from a local sbox-panel binary
  --download-url <url>     Download binary or .tar.gz archive from an explicit URL
  --proxy <prefix>         GitHub download proxy URL prefix for public repos (e.g. https://ghproxy.net/)
  --skip-verify            Skip SHA-256 verification against checksums.txt
  --uninstall              Alias for the uninstall command
  --purge                  With uninstall, also delete the data directory
  -h, --help               Show this help message
EOF
}

parse_args() {
  if [ "$#" -gt 0 ]; then
    case "$1" in
      install|upgrade|uninstall)
        ACTION="$1"
        shift
        ;;
    esac
  fi

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --version)
        [ "$#" -ge 2 ] || { log_err "--version requires a value"; exit 1; }
        VERSION="$2"
        shift 2
        ;;
      --repo)
        [ "$#" -ge 2 ] || { log_err "--repo requires a value"; exit 1; }
        REPO="$2"
        shift 2
        ;;
      --token)
        [ "$#" -ge 2 ] || { log_err "--token requires a value"; exit 1; }
        GH_TOKEN="$2"
        shift 2
        ;;
      --service-name)
        [ "$#" -ge 2 ] || { log_err "--service-name requires a value"; exit 1; }
        SERVICE_BASE="$2"
        shift 2
        ;;
      --bin-path)
        [ "$#" -ge 2 ] || { log_err "--bin-path requires a path"; exit 1; }
        INSTALL_PATH="$2"
        shift 2
        ;;
      --binary)
        [ "$#" -ge 2 ] || { log_err "--binary requires a path"; exit 1; }
        LOCAL_BINARY="$2"
        shift 2
        ;;
      --download-url)
        [ "$#" -ge 2 ] || { log_err "--download-url requires a URL"; exit 1; }
        DOWNLOAD_URL="$2"
        shift 2
        ;;
      --proxy)
        [ "$#" -ge 2 ] || { log_err "--proxy requires a URL prefix"; exit 1; }
        GH_PROXY="$2"
        shift 2
        ;;
      --data-dir)
        [ "$#" -ge 2 ] || { log_err "--data-dir requires a path"; exit 1; }
        DATA_DIR="$2"
        shift 2
        ;;
      --port)
        [ "$#" -ge 2 ] || { log_err "--port requires a port number"; exit 1; }
        PORT="$2"
        PORT_EXPLICIT=1
        shift 2
        ;;
      --listen)
        [ "$#" -ge 2 ] || { log_err "--listen requires host:port"; exit 1; }
        LISTEN_ADDR="$2"
        LISTEN_EXPLICIT=1
        shift 2
        ;;
      --skip-verify)
        SKIP_VERIFY=1
        shift
        ;;
      --uninstall)
        ACTION="uninstall"
        shift
        ;;
      --purge)
        PURGE=1
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        log_err "Unknown argument: $1"
        usage
        exit 1
        ;;
    esac
  done

  SERVICE_BASE="${SERVICE_BASE%.service}"
  if [ -z "$SERVICE_BASE" ]; then
    log_err "--service-name cannot be empty"
    exit 1
  fi
  SERVICE_NAME="${SERVICE_BASE}.service"
  SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}"

  case "$PORT" in
    ''|*[!0-9]*)
      log_err "Invalid --port '${PORT}': must be an integer between 1 and 65535"
      exit 1
      ;;
  esac
  if [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
    log_err "Invalid --port '${PORT}': must be between 1 and 65535"
    exit 1
  fi

  if [ "$LISTEN_EXPLICIT" -eq 0 ]; then
    if [ "$PORT_EXPLICIT" -eq 1 ] || [ -z "$LISTEN_ADDR" ]; then
      LISTEN_ADDR="0.0.0.0:${PORT}"
    fi
  fi
}

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    log_err "This script must be run as root (try: sudo bash $0 ...)."
    exit 1
  fi
}

require_linux_systemd() {
  if [ "$(uname -s)" != "Linux" ]; then
    log_err "Unsupported OS: $(uname -s). This installer targets Linux."
    exit 1
  fi
  if ! command -v systemctl >/dev/null 2>&1 || [ ! -d /run/systemd/system ]; then
    log_err "systemd is required on this host. For non-systemd environments, use deploy/docker-compose.yml."
    exit 1
  fi
}

detect_arch() {
  local machine
  machine="$(uname -m)"
  case "$machine" in
    x86_64|amd64)
      echo "amd64"
      ;;
    aarch64|arm64)
      echo "arm64"
      ;;
    *)
      log_err "Unsupported architecture: ${machine} (supported: amd64, arm64)"
      exit 1
      ;;
  esac
}

apply_proxy() {
  local url="$1"
  if [ -n "$GH_PROXY" ]; then
    printf "%s/%s" "${GH_PROXY%/}" "$url"
  else
    printf "%s" "$url"
  fi
}

http_get_public() {
  local url="$1"
  local dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 --connect-timeout 15 -o "$dest" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" --tries=3 --timeout=15 "$url"
  else
    log_err "Neither curl nor wget is installed. Please install curl and retry."
    exit 1
  fi
}

http_get_github_json() {
  local url="$1"
  local dest="$2"
  if command -v curl >/dev/null 2>&1; then
    if [ -n "$GH_TOKEN" ]; then
      curl -fsSL --retry 3 --connect-timeout 15 \
        -H "Authorization: Bearer ${GH_TOKEN}" \
        -H "Accept: application/vnd.github+json" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        -o "$dest" "$url"
    else
      curl -fsSL --retry 3 --connect-timeout 15 \
        -H "Accept: application/vnd.github+json" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        -o "$dest" "$url"
    fi
  elif command -v wget >/dev/null 2>&1; then
    if [ -n "$GH_TOKEN" ]; then
      wget -qO "$dest" --tries=3 --timeout=15 \
        --header="Authorization: Bearer ${GH_TOKEN}" \
        --header="Accept: application/vnd.github+json" \
        --header="X-GitHub-Api-Version: 2022-11-28" \
        "$url"
    else
      wget -qO "$dest" --tries=3 --timeout=15 \
        --header="Accept: application/vnd.github+json" \
        --header="X-GitHub-Api-Version: 2022-11-28" \
        "$url"
    fi
  else
    log_err "Neither curl nor wget is installed. Please install curl and retry."
    exit 1
  fi
}

http_get_github_asset() {
  local api_url="$1"
  local browser_url="$2"
  local dest="$3"

  if [ -n "$GH_TOKEN" ]; then
    if [ -n "$GH_PROXY" ]; then
      log_warn "Ignoring --proxy for authenticated GitHub API download to avoid exposing token."
    fi
    if command -v curl >/dev/null 2>&1; then
      # curl automatically omits Authorization header when redirected to S3 storage
      curl -fsSL --retry 3 --connect-timeout 15 \
        -H "Authorization: Bearer ${GH_TOKEN}" \
        -H "Accept: application/octet-stream" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        -o "$dest" "$api_url"
    elif command -v wget >/dev/null 2>&1; then
      # Resolve 302 redirect first so Authorization header is not forwarded to S3
      local headers redirect_url
      headers="$(wget --max-redirect=0 --server-response --timeout=15 \
        --header="Authorization: Bearer ${GH_TOKEN}" \
        --header="Accept: application/octet-stream" \
        --header="X-GitHub-Api-Version: 2022-11-28" \
        -O /dev/null "$api_url" 2>&1 || true)"
      redirect_url="$(printf "%s\n" "$headers" \
        | awk '/^[[:space:]]*[Ll]ocation:[[:space:]]*/ { sub(/^[[:space:]]*[Ll]ocation:[[:space:]]*/, ""); sub(/\r$/, ""); print; exit }')"
      if [ -n "$redirect_url" ]; then
        wget -qO "$dest" --tries=3 --timeout=15 "$redirect_url"
      else
        wget -qO "$dest" --tries=3 --timeout=15 \
          --header="Authorization: Bearer ${GH_TOKEN}" \
          --header="Accept: application/octet-stream" \
          --header="X-GitHub-Api-Version: 2022-11-28" \
          "$api_url"
      fi
    else
      log_err "Neither curl nor wget is installed. Please install curl and retry."
      exit 1
    fi
  else
    local url
    url="$(apply_proxy "$browser_url")"
    http_get_public "$url" "$dest"
  fi
}

# Prints "name<TAB>api_url<TAB>browser_download_url" for the first matching asset.
find_release_asset() {
  local release_json="$1"
  local pattern="$2"

  if command -v jq >/dev/null 2>&1; then
    jq -r --arg re "$pattern" '
      (if type == "array" then (.[0] // {}) else . end)
      | (.assets // [])
      | map(select((.name // "") | test($re)))
      | .[0] // empty
      | [(.name // ""), (.url // ""), (.browser_download_url // "")]
      | @tsv
    ' "$release_json"
    return 0
  fi

  if command -v python3 >/dev/null 2>&1; then
    python3 - "$release_json" "$pattern" <<'PYEOF'
import json, re, sys
with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)
if isinstance(data, list):
    data = data[0] if data else {}
pat = re.compile(sys.argv[2])
for asset in data.get("assets") or []:
    name = asset.get("name") or ""
    if pat.search(name):
        print(f"{name}\t{asset.get('url') or ''}\t{asset.get('browser_download_url') or ''}")
        break
PYEOF
    return 0
  fi

  # Pure POSIX awk fallback (handles both pretty-printed and minified GitHub API JSON)
  sed -E 's/,[[:space:]]*"/,\n"/g; s/\{/{\n/g; s/\}/\n}/g' "$release_json" \
    | awk -v re="$pattern" '
      function flush_asset() {
        if (in_asset && asset_name != "" && asset_name ~ re) {
          printf "%s\t%s\t%s\n", asset_name, asset_api, asset_dl
          found = 1
          exit 0
        }
        in_asset = 0; asset_name = ""; asset_api = ""; asset_dl = ""
      }
      /"url"[[:space:]]*:[[:space:]]*"https:\/\/api\.github\.com\/repos\/[^"]+\/releases\/assets\/[0-9]+"/ {
        flush_asset()
        in_asset = 1
        val = $0
        sub(/^.*"url"[[:space:]]*:[[:space:]]*"/, "", val)
        sub(/".*$/, "", val)
        asset_api = val
        next
      }
      in_asset && asset_name == "" && /"name"[[:space:]]*:[[:space:]]*"[^"]+"/ {
        val = $0
        sub(/^.*"name"[[:space:]]*:[[:space:]]*"/, "", val)
        sub(/".*$/, "", val)
        asset_name = val
        next
      }
      in_asset && /"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]+"/ {
        val = $0
        sub(/^.*"browser_download_url"[[:space:]]*:[[:space:]]*"/, "", val)
        sub(/".*$/, "", val)
        asset_dl = val
        flush_asset()
        next
      }
      END {
        if (!found) flush_asset()
      }
    '
}

compute_sha256() {
  local file_path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file_path" | awk '{print tolower($1)}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file_path" | awk '{print tolower($1)}'
  else
    return 1
  fi
}

verify_checksum() {
  local artifact="$1"
  local asset_name="$2"
  local checksums_file="$3"

  if [ "$SKIP_VERIFY" -eq 1 ]; then
    log_warn "Skipping SHA-256 verification (--skip-verify)."
    return 0
  fi

  local expected actual
  expected="$(awk -v target="$asset_name" '
    /^[[:space:]]*$/ || /^[[:space:]]*#/ { next }
    {
      hash = tolower($1)
      file = $2
      sub(/\r$/, "", file)
      sub(/^\*/, "", file)
      sub(/^\.\//, "", file)
      sub(/^.*\//, "", file)
      if (file == target) {
        print hash
        exit 0
      }
    }
  ' "$checksums_file")"

  if [ -z "$expected" ]; then
    log_warn "No checksum entry for '${asset_name}' in checksums.txt; skipping verification."
    return 0
  fi

  if ! actual="$(compute_sha256 "$artifact")"; then
    log_warn "Neither sha256sum nor shasum found; skipping SHA-256 verification."
    return 0
  fi

  if [ "$actual" != "$expected" ]; then
    log_err "SHA-256 checksum mismatch for ${asset_name}!"
    log_err "  Expected: ${expected}"
    log_err "  Actual  : ${actual}"
    exit 1
  fi

  log_ok "SHA-256 verified (${asset_name}): ${actual}"
}

prepare_binary() {
  local arch="$1"
  local staged_bin="$2"

  if [ -n "$LOCAL_BINARY" ]; then
    if [ ! -f "$LOCAL_BINARY" ]; then
      log_err "Local binary not found: ${LOCAL_BINARY}"
      exit 1
    fi
    log_info "Using local binary: ${LOCAL_BINARY}"
    cp "$LOCAL_BINARY" "$staged_bin"
  elif [ -n "$DOWNLOAD_URL" ]; then
    local url artifact="${TMP_DIR}/artifact"
    url="$(apply_proxy "$DOWNLOAD_URL")"
    log_info "Downloading sbox-panel (${arch}) from ${url}..."
    http_get_public "$url" "$artifact"
    unpack_or_copy "$artifact" "$url" "$staged_bin"
  else
    local api_url release_json="${TMP_DIR}/release.json"
    if [ "$VERSION" = "latest" ]; then
      api_url="https://api.github.com/repos/${REPO}/releases/latest"
    else
      api_url="https://api.github.com/repos/${REPO}/releases/tags/${VERSION}"
    fi

    log_info "Querying GitHub Release metadata for ${REPO} (${VERSION})..."
    if ! http_get_github_json "$api_url" "$release_json"; then
      if [ "$VERSION" = "latest" ]; then
        log_warn "No stable release found via /releases/latest; falling back to most recent release (including pre-releases)..."
        if ! http_get_github_json "https://api.github.com/repos/${REPO}/releases?per_page=1" "$release_json"; then
          log_err "Failed to fetch release metadata for ${REPO} (${VERSION})."
          log_err "Tip: if the repo is private pass --token <gh_token>, or pass --binary <path>."
          exit 1
        fi
      else
        log_err "Failed to fetch release metadata for ${REPO} (${VERSION})."
        log_err "Tip: check --repo / --version, or pass --token <gh_token> for private repos, or --binary <path>."
        exit 1
      fi
    fi

    local asset_line="" asset_name="" asset_api="" asset_dl=""
    # 1. Exact release workflow asset name: sbox-panel-linux-<arch>
    asset_line="$(find_release_asset "$release_json" "^${BINARY_NAME}-linux-${arch}$" || true)"
    # 2. Fallback: archive or binary matching linux[_-]<arch> (excludes .sha256/.txt)
    if [ -z "$asset_line" ]; then
      asset_line="$(find_release_asset "$release_json" "linux[_-]${arch}(\.tar\.gz|\.tgz)?$" || true)"
    fi

    if [ -z "$asset_line" ]; then
      log_err "Could not find a linux/${arch} release asset in ${REPO} (${VERSION})."
      log_err "Tip: pass --binary /path/to/sbox-panel or --download-url <url>."
      exit 1
    fi

    IFS=$'\t' read -r asset_name asset_api asset_dl <<< "$asset_line"

    if [ -n "$GH_TOKEN" ] && [ -z "$asset_api" ]; then
      log_err "Release asset '${asset_name}' is missing API URL required for authenticated download."
      exit 1
    fi
    if [ -z "$GH_TOKEN" ] && [ -z "$asset_dl" ]; then
      log_err "Release asset '${asset_name}' is missing browser_download_url."
      exit 1
    fi

    local artifact="${TMP_DIR}/${asset_name}"
    if [ -n "$GH_TOKEN" ]; then
      log_info "Downloading ${asset_name} via authenticated GitHub Releases API..."
    else
      log_info "Downloading ${asset_name} (${arch}) from GitHub Release..."
    fi
    http_get_github_asset "$asset_api" "$asset_dl" "$artifact"

    # Verify SHA-256 against checksums.txt when available in the release
    if [ "$SKIP_VERIFY" -eq 0 ]; then
      local cs_line="" cs_name="" cs_api="" cs_dl=""
      cs_line="$(find_release_asset "$release_json" "^(checksums\.txt|SHA256SUMS(\.txt)?)$" || true)"
      if [ -n "$cs_line" ]; then
        IFS=$'\t' read -r cs_name cs_api cs_dl <<< "$cs_line"
        local cs_file="${TMP_DIR}/${cs_name}"
        log_info "Downloading ${cs_name} for SHA-256 verification..."
        if http_get_github_asset "$cs_api" "$cs_dl" "$cs_file"; then
          verify_checksum "$artifact" "$asset_name" "$cs_file"
        else
          log_warn "Failed to download ${cs_name}; skipping SHA-256 verification."
        fi
      else
        log_warn "No checksums.txt asset found in release; skipping SHA-256 verification."
      fi
    else
      log_warn "Skipping SHA-256 verification (--skip-verify)."
    fi

    unpack_or_copy "$artifact" "$asset_name" "$staged_bin"
  fi

  if [ ! -s "$staged_bin" ]; then
    log_err "Prepared binary is empty."
    exit 1
  fi
  chmod 0755 "$staged_bin"
}

unpack_or_copy() {
  local artifact="$1"
  local name_or_url="$2"
  local staged_bin="$3"

  case "$name_or_url" in
    *.tar.gz|*.tgz)
      mkdir -p "${TMP_DIR}/extract"
      tar -xzf "$artifact" -C "${TMP_DIR}/extract"
      local extracted
      extracted="$(find "${TMP_DIR}/extract" -type f -name "${BINARY_NAME}" | head -n 1 || true)"
      if [ -z "$extracted" ]; then
        extracted="$(find "${TMP_DIR}/extract" -type f -name "${BINARY_NAME}*" ! -name "*.txt" ! -name "*.md" ! -name "*.sha256" ! -name "*.service" | head -n 1 || true)"
      fi
      if [ -z "$extracted" ]; then
        log_err "Archive did not contain '${BINARY_NAME}' binary."
        exit 1
      fi
      cp "$extracted" "$staged_bin"
      ;;
    *)
      cp "$artifact" "$staged_bin"
      ;;
  esac
}

write_systemd_unit() {
  local state_dir_block=""
  if [ "$DATA_DIR" = "/var/lib/${SERVICE_BASE}" ]; then
    state_dir_block=$'StateDirectory='"${SERVICE_BASE}"$'\nStateDirectoryMode=0750'
  fi

  cat > "$SERVICE_PATH" <<EOF
[Unit]
Description=sbox-panel - Lightweight sing-box Management Panel
Documentation=https://github.com/${REPO}
After=network-online.target nss-lookup.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
WorkingDirectory=${DATA_DIR}
${state_dir_block}
EnvironmentFile=-/etc/default/${SERVICE_BASE}
ExecStart=${INSTALL_PATH} serve --data-dir ${DATA_DIR} --listen ${LISTEN_ADDR}

Restart=on-failure
RestartSec=5s
TimeoutStopSec=15s
KillMode=mixed

LimitNOFILE=1048576
TasksMax=4096

CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
ReadWritePaths=${DATA_DIR}

StandardOutput=journal
StandardError=journal
SyslogIdentifier=${SERVICE_BASE}

[Install]
WantedBy=multi-user.target
EOF

  chmod 0644 "$SERVICE_PATH"
}

wait_and_show_url() {
  local tries=20
  local i=0
  local url_output=""

  while [ "$i" -lt "$tries" ]; do
    if ! systemctl is-active --quiet "$SERVICE_NAME" || [ ! -f "${DATA_DIR}/panel.db" ]; then
      sleep 0.5
      i=$((i + 1))
      continue
    fi
    if url_output="$("$INSTALL_PATH" show-url --data-dir "$DATA_DIR" 2>/dev/null)" && [ -n "$url_output" ]; then
      printf "\n"
      log_ok "${SERVICE_BASE} is running!"
      printf "============================================================\n"
      printf "%s\n" "$url_output"
      printf "============================================================\n"
      printf "Binary   : %s\n" "$INSTALL_PATH"
      printf "Data dir : %s\n" "$DATA_DIR"
      printf "Service  : systemctl status %s\n" "$SERVICE_NAME"
      printf "Logs     : journalctl -u %s -f\n" "$SERVICE_NAME"
      printf "Rescue   : %s reset-auth --data-dir %s\n" "$INSTALL_PATH" "$DATA_DIR"
      return 0
    fi
    sleep 0.5
    i=$((i + 1))
  done

  if systemctl is-active --quiet "$SERVICE_NAME"; then
    log_warn "Service is active, running show-url directly:"
    "$INSTALL_PATH" show-url --data-dir "$DATA_DIR" || true
  else
    log_err "${SERVICE_NAME} failed to stay running. Recent logs:"
    journalctl -u "$SERVICE_NAME" -n 30 --no-pager >&2 || true
    return 1
  fi
}

do_install_or_upgrade() {
  require_root
  require_linux_systemd

  local arch is_upgrade=0 backup_path="${INSTALL_PATH}.bak"
  arch="$(detect_arch)"
  TMP_DIR="$(mktemp -d)"

  if [ -f "$INSTALL_PATH" ] || [ -f "$SERVICE_PATH" ]; then
    is_upgrade=1
    log_info "Existing ${SERVICE_BASE} installation detected; performing in-place upgrade (preserving ${DATA_DIR})."
  else
    log_info "Installing ${SERVICE_BASE} for linux/${arch}..."
  fi

  local staged_bin="${TMP_DIR}/${BINARY_NAME}"
  prepare_binary "$arch" "$staged_bin"

  mkdir -p "$(dirname "$INSTALL_PATH")"
  mkdir -p "$DATA_DIR"
  chmod 0750 "$DATA_DIR"

  local was_active=0
  if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
    was_active=1
    log_info "Stopping running ${SERVICE_NAME} before binary swap..."
    systemctl stop "$SERVICE_NAME"
  fi

  if [ -f "$INSTALL_PATH" ]; then
    cp -p "$INSTALL_PATH" "$backup_path"
  fi

  install -m 0755 "$staged_bin" "${INSTALL_PATH}.new"
  mv -f "${INSTALL_PATH}.new" "$INSTALL_PATH"

  write_systemd_unit
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME" >/dev/null

  log_info "Starting ${SERVICE_NAME}..."
  if ! systemctl restart "$SERVICE_NAME"; then
    log_err "Failed to start ${SERVICE_NAME} with the new binary."
    if [ "$is_upgrade" -eq 1 ] && [ -f "$backup_path" ]; then
      log_warn "Restoring previous binary from ${backup_path}..."
      mv -f "$backup_path" "$INSTALL_PATH"
      if [ "$was_active" -eq 1 ]; then
        systemctl restart "$SERVICE_NAME" || true
      fi
    fi
    journalctl -u "$SERVICE_NAME" -n 30 --no-pager >&2 || true
    exit 1
  fi

  if wait_and_show_url; then
    rm -f "$backup_path"
  else
    if [ "$is_upgrade" -eq 1 ] && [ -f "$backup_path" ]; then
      log_warn "Rolling back to previous binary from ${backup_path}..."
      mv -f "$backup_path" "$INSTALL_PATH"
      systemctl restart "$SERVICE_NAME" || true
    fi
    exit 1
  fi
}

do_uninstall() {
  require_root
  require_linux_systemd

  log_info "Uninstalling ${SERVICE_BASE}..."
  if systemctl list-unit-files "$SERVICE_NAME" >/dev/null 2>&1; then
    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    systemctl disable "$SERVICE_NAME" 2>/dev/null || true
  fi

  rm -f "$SERVICE_PATH"
  systemctl daemon-reload
  systemctl reset-failed "$SERVICE_NAME" 2>/dev/null || true

  rm -f "$INSTALL_PATH" "${INSTALL_PATH}.bak" "${INSTALL_PATH}.new"

  if [ "$PURGE" -eq 1 ]; then
    if [ -n "$DATA_DIR" ] && [ "$DATA_DIR" != "/" ]; then
      log_warn "Removing data directory ${DATA_DIR} (--purge specified)..."
      rm -rf "$DATA_DIR"
    fi
  else
    log_info "Preserved data directory: ${DATA_DIR} (pass --purge to delete it)."
  fi

  log_ok "${SERVICE_BASE} uninstalled."
}

main() {
  parse_args "$@"
  case "$ACTION" in
    install|upgrade)
      do_install_or_upgrade
      ;;
    uninstall)
      do_uninstall
      ;;
    *)
      log_err "Unsupported action: ${ACTION}"
      exit 1
      ;;
  esac
}

main "$@"
