#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# Rapid VPS Test Deployment Helper for sbox-panel
# Cross-compiles a static Linux binary (optional) and deploys/restarts it over
# SSH into /opt/sbox-panel-test on a test VPS alias (default: sbox-test).
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

VPS_HOST="${SBOX_VPS_HOST:-sbox-test}"
REMOTE_DIR="${SBOX_REMOTE_DIR:-/opt/sbox-panel-test}"
LISTEN_ADDR="${SBOX_LISTEN:-0.0.0.0:18080}"
TARGET_ARCH="${SBOX_ARCH:-auto}"
VERSION="${SBOX_VERSION:-0.1.0}"
BUILD_WEB=0
SKIP_BUILD=0
BINARY_PATH=""
USE_SYSTEMD="auto"
TMP_DIR=""
GO_BIN="${GO_BIN:-}"

log()  { printf "[push-vps] %s\n" "$*"; }
fail() { printf "[push-vps] ERROR: %s\n" "$*" >&2; exit 1; }

cleanup() {
  if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

usage() {
  cat <<EOF
Rapidly cross-compile and deploy sbox-panel to a remote test VPS over SSH.

Usage:
  bash deploy/push-vps-test.sh [options]

Options:
  --host <ssh-alias>     SSH host alias or user@host (default: ${VPS_HOST})
  --remote-dir <path>    Remote test directory (default: ${REMOTE_DIR})
  --listen <addr>        Listen address on remote VPS (default: ${LISTEN_ADDR})
  --arch <auto|amd64|arm64>
                         Target Linux GOARCH; 'auto' detects remote uname -m (default: ${TARGET_ARCH})
  --version <ver>        Version string injected into main.Version (default: ${VERSION})
  --build-web            Force running 'npm run build' in web/ before Go build
  --skip-build           Skip Go compilation and upload an existing binary (--binary)
  --binary <path>        Path to prebuilt Linux sbox-panel binary (implies --skip-build)
  --no-systemd           Run via nohup + PID file instead of systemd even if root
  -h, --help             Show this help message
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --host)
      [ "$#" -ge 2 ] || fail "--host requires a value"
      VPS_HOST="$2"
      shift 2
      ;;
    --remote-dir)
      [ "$#" -ge 2 ] || fail "--remote-dir requires a value"
      REMOTE_DIR="$2"
      shift 2
      ;;
    --listen)
      [ "$#" -ge 2 ] || fail "--listen requires a value"
      LISTEN_ADDR="$2"
      shift 2
      ;;
    --arch)
      [ "$#" -ge 2 ] || fail "--arch requires auto|amd64|arm64"
      TARGET_ARCH="$2"
      shift 2
      ;;
    --version)
      [ "$#" -ge 2 ] || fail "--version requires a value"
      VERSION="$2"
      shift 2
      ;;
    --build-web)
      BUILD_WEB=1
      shift
      ;;
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --binary)
      [ "$#" -ge 2 ] || fail "--binary requires a path"
      BINARY_PATH="$2"
      SKIP_BUILD=1
      shift 2
      ;;
    --no-systemd)
      USE_SYSTEMD="no"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "Unknown option: $1"
      ;;
  esac
done

command -v ssh >/dev/null 2>&1 || fail "ssh not found in PATH"
command -v scp >/dev/null 2>&1 || fail "scp not found in PATH"

if [ "$SKIP_BUILD" -eq 0 ]; then
  if [ -z "$GO_BIN" ]; then
    if command -v go >/dev/null 2>&1; then
      GO_BIN="go"
    elif [ -x "${REPO_ROOT}/../.tools/go/bin/go" ]; then
      GO_BIN="${REPO_ROOT}/../.tools/go/bin/go"
    elif [ -x "${REPO_ROOT}/../.tools/go/bin/go.exe" ]; then
      GO_BIN="${REPO_ROOT}/../.tools/go/bin/go.exe"
    else
      fail "go not found in PATH (set GO_BIN or use --binary <path>)"
    fi
  fi

  if [ "$TARGET_ARCH" = "auto" ]; then
    log "Detecting remote architecture on ${VPS_HOST}..."
    REMOTE_UNAME="$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$VPS_HOST" "uname -m" | tr -d '\r\n')"
    case "$REMOTE_UNAME" in
      x86_64|amd64) TARGET_ARCH="amd64" ;;
      aarch64|arm64) TARGET_ARCH="arm64" ;;
      *) fail "Unsupported remote arch: ${REMOTE_UNAME}" ;;
    esac
  fi

  case "$TARGET_ARCH" in
    amd64|arm64) ;;
    *) fail "Invalid --arch '${TARGET_ARCH}' (expected auto, amd64, or arm64)" ;;
  esac

  if [ "$BUILD_WEB" -eq 1 ] || [ ! -f "${REPO_ROOT}/web/dist/index.html" ]; then
    command -v npm >/dev/null 2>&1 || fail "npm not found in PATH (needed to build web/dist)"
    log "Building frontend in web/..."
    (cd "${REPO_ROOT}/web" && npm run build)
  fi

  TMP_DIR="$(mktemp -d)"
  BINARY_PATH="${TMP_DIR}/sbox-panel-linux-${TARGET_ARCH}"

  log "Cross-compiling linux/${TARGET_ARCH} static binary..."
  (
    cd "$REPO_ROOT"
    CGO_ENABLED=0 GOOS=linux GOARCH="$TARGET_ARCH" \
      "$GO_BIN" build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o "$BINARY_PATH" ./cmd/sbox-panel
  )
else
  [ -n "$BINARY_PATH" ] || fail "--skip-build requires --binary <path>"
  [ -f "$BINARY_PATH" ] || fail "Binary not found: ${BINARY_PATH}"
fi

log "Preparing ${REMOTE_DIR} on ${VPS_HOST}..."
ssh -o ConnectTimeout=10 "$VPS_HOST" "mkdir -p '${REMOTE_DIR}/data'"

log "Uploading binary to ${VPS_HOST}:${REMOTE_DIR}/sbox-panel.new..."
scp "$BINARY_PATH" "${VPS_HOST}:${REMOTE_DIR}/sbox-panel.new"

log "Deploying and restarting test instance on ${VPS_HOST}..."
ssh "$VPS_HOST" "REMOTE_DIR='${REMOTE_DIR}' LISTEN_ADDR='${LISTEN_ADDR}' USE_SYSTEMD='${USE_SYSTEMD}' bash -s" <<'REMOTE_EOF'
set -euo pipefail

BIN="${REMOTE_DIR}/sbox-panel"
DATA_DIR="${REMOTE_DIR}/data"
PID_FILE="${REMOTE_DIR}/sbox-panel.pid"
LOG_FILE="${REMOTE_DIR}/sbox-panel.log"
SERVICE_NAME="sbox-panel-test.service"
SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}"

chmod 0755 "${REMOTE_DIR}/sbox-panel.new"

use_sysd=0
if [ "$USE_SYSTEMD" != "no" ] && [ "$(id -u)" -eq 0 ] && command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  use_sysd=1
fi

if [ "$use_sysd" -eq 1 ]; then
  systemctl stop "$SERVICE_NAME" 2>/dev/null || true
  mv -f "${REMOTE_DIR}/sbox-panel.new" "$BIN"
  cat > "$SERVICE_PATH" <<UNIT_EOF
[Unit]
Description=sbox-panel (VPS Test Instance)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${DATA_DIR}
ExecStart=${BIN} serve --data-dir ${DATA_DIR} --listen ${LISTEN_ADDR}
Restart=on-failure
RestartSec=3s
LimitNOFILE=1048576
AmbientCapabilities=CAP_NET_BIND_SERVICE
KillMode=mixed

[Install]
WantedBy=multi-user.target
UNIT_EOF
  systemctl daemon-reload
  systemctl restart "$SERVICE_NAME"
  sleep 1
  if ! systemctl is-active --quiet "$SERVICE_NAME"; then
    echo "ERROR: ${SERVICE_NAME} failed to start" >&2
    journalctl -u "$SERVICE_NAME" -n 30 --no-pager >&2 || true
    exit 1
  fi
else
  if [ -f "$PID_FILE" ]; then
    old_pid="$(cat "$PID_FILE" 2>/dev/null || true)"
    if [ -n "$old_pid" ] && kill -0 "$old_pid" 2>/dev/null; then
      kill "$old_pid" 2>/dev/null || true
      for _ in 1 2 3 4 5 6 7 8 9 10; do
        kill -0 "$old_pid" 2>/dev/null || break
        sleep 0.3
      done
      kill -9 "$old_pid" 2>/dev/null || true
    fi
    rm -f "$PID_FILE"
  fi

  mv -f "${REMOTE_DIR}/sbox-panel.new" "$BIN"
  nohup "$BIN" serve --data-dir "$DATA_DIR" --listen "$LISTEN_ADDR" >"$LOG_FILE" 2>&1 </dev/null &
  new_pid=$!
  printf "%s\n" "$new_pid" > "$PID_FILE"
  sleep 1
  if ! kill -0 "$new_pid" 2>/dev/null; then
    echo "ERROR: sbox-panel exited unexpectedly. Recent logs:" >&2
    tail -n 30 "$LOG_FILE" >&2 || true
    exit 1
  fi
fi

echo "--- Remote sbox-panel URL ---"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if [ -f "${DATA_DIR}/panel.db" ] && "$BIN" show-url --data-dir "$DATA_DIR" 2>/dev/null; then
    exit 0
  fi
  sleep 0.5
done
"$BIN" show-url --data-dir "$DATA_DIR"
REMOTE_EOF

log "Done."
