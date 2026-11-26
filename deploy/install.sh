#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# sbox-panel Linux One-Click Installer / Upgrader / Uninstaller
# Supports Linux amd64 & arm64 with systemd.
# Preserves /var/lib/sbox-panel data across upgrades (and uninstalls unless --purge).
# ==============================================================================

BINARY_NAME="sbox-panel"
INSTALL_PATH="/usr/local/bin/${BINARY_NAME}"
SERVICE_NAME="sbox-panel.service"
SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}"
DATA_DIR="/var/lib/sbox-panel"
LISTEN_ADDR="0.0.0.0:18080"
REPO="${SBOX_REPO:-sbox-panel/sbox-panel}"
VERSION="${SBOX_VERSION:-latest}"
DOWNLOAD_URL="${SBOX_DOWNLOAD_URL:-}"
LOCAL_BINARY=""
GH_PROXY="${SBOX_GH_PROXY:-}"
ACTION="install"
PURGE=0
TMP_DIR=""

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
  install                Install sbox-panel (or upgrade in-place if already installed) [default]
  upgrade                Upgrade binary and refresh systemd unit while preserving data
  uninstall              Stop/remove service and binary (preserves data unless --purge)

Options:
  --version <tag>        Release tag to install (default: latest)
  --repo <owner/repo>    GitHub repository (default: ${REPO})
  --binary <path>        Install/upgrade directly from a local sbox-panel binary
  --download-url <url>   Download binary or .tar.gz archive from an explicit URL
  --proxy <prefix>       GitHub download proxy URL prefix (e.g. https://ghproxy.net/)
  --data-dir <dir>       Persistent data directory (default: ${DATA_DIR})
  --listen <addr>        Panel listen address (default: ${LISTEN_ADDR})
  --uninstall            Alias for the uninstall command
  --purge                With uninstall, also delete the data directory (${DATA_DIR})
  -h, --help             Show this help message
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
      --listen)
        [ "$#" -ge 2 ] || { log_err "--listen requires host:port"; exit 1; }
        LISTEN_ADDR="$2"
        shift 2
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

http_get() {
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

apply_proxy() {
  local url="$1"
  if [ -n "$GH_PROXY" ]; then
    printf "%s/%s" "${GH_PROXY%/}" "$url"
  else
    printf "%s" "$url"
  fi
}

resolve_release_url() {
  local arch="$1"
  local api_url release_json asset_url

  if [ "$VERSION" = "latest" ]; then
    api_url="https://api.github.com/repos/${REPO}/releases/latest"
  else
    api_url="https://api.github.com/repos/${REPO}/releases/tags/${VERSION}"
  fi

  release_json="${TMP_DIR}/release.json"
  log_info "Querying release metadata from ${api_url}..." >&2
  if ! http_get "$api_url" "$release_json"; then
    log_err "Failed to fetch release metadata for ${REPO} (${VERSION})."
    log_err "Tip: pass --binary /path/to/sbox-panel or --download-url <url> if installing a custom/unreleased build."
    exit 1
  fi

  # Prefer .tar.gz/.tgz archive or raw binary matching linux + arch (excludes .sha256)
  asset_url="$(grep -oE '"browser_download_url":[[:space:]]*"[^"]+"' "$release_json" \
    | sed -E 's/^"browser_download_url":[[:space:]]*"([^"]+)"$/\1/' \
    | grep -E "linux[_-]${arch}(\.tar\.gz|\.tgz)?$" \
    | head -n 1 || true)"

  if [ -z "$asset_url" ]; then
    log_err "Could not find a linux/${arch} release asset in ${REPO} (${VERSION})."
    log_err "Tip: pass --binary /path/to/sbox-panel or --download-url <url>."
    exit 1
  fi

  printf "%s" "$asset_url"
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
  else
    local url
    if [ -n "$DOWNLOAD_URL" ]; then
      url="$DOWNLOAD_URL"
    else
      url="$(resolve_release_url "$arch")"
    fi
    url="$(apply_proxy "$url")"

    local artifact="${TMP_DIR}/artifact"
    log_info "Downloading sbox-panel (${arch}) from ${url}..."
    http_get "$url" "$artifact"

    case "$url" in
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
  fi

  if [ ! -s "$staged_bin" ]; then
    log_err "Prepared binary is empty."
    exit 1
  fi
  chmod 0755 "$staged_bin"
}

write_systemd_unit() {
  local state_dir_block=""
  if [ "$DATA_DIR" = "/var/lib/sbox-panel" ]; then
    state_dir_block=$'StateDirectory=sbox-panel\nStateDirectoryMode=0750'
  fi

  cat > "$SERVICE_PATH" <<EOF
[Unit]
Description=sbox-panel - Lightweight sing-box Management Panel
Documentation=https://github.com/sbox-panel/sbox-panel
After=network-online.target nss-lookup.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
WorkingDirectory=${DATA_DIR}
${state_dir_block}
EnvironmentFile=-/etc/default/sbox-panel
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
SyslogIdentifier=sbox-panel

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
      log_ok "sbox-panel is running!"
      printf "============================================================\n"
      printf "%s\n" "$url_output"
      printf "============================================================\n"
      printf "Data dir : %s\n" "$DATA_DIR"
      printf "Service  : systemctl status %s\n" "$SERVICE_NAME"
      printf "Logs     : journalctl -u %s -f\n" "$SERVICE_NAME"
      printf "Rescue   : %s reset-auth --data-dir %s\n" "$BINARY_NAME" "$DATA_DIR"
      return 0
    fi
    sleep 0.5
    i=$((i + 1))
  done

  if systemctl is-active --quiet "$SERVICE_NAME"; then
    log_warn "Service is active, running show-url directly:"
    "$INSTALL_PATH" show-url --data-dir "$DATA_DIR" || true
  else
    log_err "sbox-panel failed to stay running. Recent logs:"
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
    log_info "Existing sbox-panel installation detected; performing in-place upgrade (preserving ${DATA_DIR})."
  else
    log_info "Installing sbox-panel for linux/${arch}..."
  fi

  local staged_bin="${TMP_DIR}/${BINARY_NAME}"
  prepare_binary "$arch" "$staged_bin"

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

  log_info "Uninstalling sbox-panel..."
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

  log_ok "sbox-panel uninstalled."
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
