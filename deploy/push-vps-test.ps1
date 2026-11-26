<#
.SYNOPSIS
    Cross-compile and deploy sbox-panel to a remote test VPS over SSH.
.DESCRIPTION
    Builds a static Linux binary (CGO_ENABLED=0 GOOS=linux GOARCH=<arch>) from
    this repository (unless -SkipBuild is specified), uploads it via scp to an
    SSH host alias (default: sbox-test) under /opt/sbox-panel-test, restarts
    the remote test instance, and prints the live secret panel URL.
.EXAMPLE
    .\deploy\push-vps-test.ps1
.EXAMPLE
    .\deploy\push-vps-test.ps1 -HostAlias sbox-test -Arch arm64 -BuildWeb
.EXAMPLE
    .\deploy\push-vps-test.ps1 -HostAlias root@1.2.3.4 -SkipBuild -Binary .\sbox-panel-linux-amd64
#>
[CmdletBinding()]
param(
    [string]$HostAlias = $(if ($env:SBOX_VPS_HOST) { $env:SBOX_VPS_HOST } else { "sbox-test" }),
    [string]$RemoteDir = $(if ($env:SBOX_REMOTE_DIR) { $env:SBOX_REMOTE_DIR } else { "/opt/sbox-panel-test" }),
    [string]$ListenAddr = $(if ($env:SBOX_LISTEN) { $env:SBOX_LISTEN } else { "0.0.0.0:18080" }),
    [ValidateSet("auto", "amd64", "arm64")]
    [string]$Arch = $(if ($env:SBOX_ARCH) { $env:SBOX_ARCH } else { "auto" }),
    [string]$Version = $(if ($env:SBOX_VERSION) { $env:SBOX_VERSION } else { "0.1.0" }),
    [switch]$BuildWeb,
    [switch]$SkipBuild,
    [string]$Binary = "",
    [switch]$NoSystemd
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Write-Step([string]$Message) {
    Write-Host "[push-vps] $Message" -ForegroundColor Cyan
}

function Require-Command([string]$Name) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command '$Name' was not found in PATH."
    }
}

Require-Command "ssh"
Require-Command "scp"

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$TempDir = $null

try {
    if ($Binary) {
        $SkipBuild = $true
    }

    if (-not $SkipBuild) {
        $GoCmd = Get-Command "go" -ErrorAction SilentlyContinue
        if ($GoCmd) {
            $GoExe = $GoCmd.Source
        } else {
            $WorkspaceGo = Join-Path (Split-Path $RepoRoot -Parent) ".tools\go\bin\go.exe"
            if (Test-Path $WorkspaceGo) {
                $GoExe = $WorkspaceGo
            } else {
                throw "Required command 'go' was not found in PATH or '$WorkspaceGo' (or use -SkipBuild -Binary <path>)."
            }
        }

        if ($Arch -eq "auto") {
            Write-Step "Detecting remote architecture on $HostAlias..."
            $RemoteUname = (& ssh -o BatchMode=yes -o ConnectTimeout=10 $HostAlias "uname -m").Trim()
            if ($LASTEXITCODE -ne 0) {
                throw "Failed to detect remote architecture via SSH on '$HostAlias'."
            }
            switch ($RemoteUname) {
                "x86_64"  { $Arch = "amd64" }
                "amd64"   { $Arch = "amd64" }
                "aarch64" { $Arch = "arm64" }
                "arm64"   { $Arch = "arm64" }
                default   { throw "Unsupported remote architecture: $RemoteUname" }
            }
        }

        $WebDistIndex = Join-Path $RepoRoot "web\dist\index.html"
        if ($BuildWeb -or -not (Test-Path $WebDistIndex)) {
            Require-Command "npm"
            Write-Step "Building frontend in web/..."
            Push-Location (Join-Path $RepoRoot "web")
            try {
                & npm run build
                if ($LASTEXITCODE -ne 0) { throw "npm run build failed." }
            } finally {
                Pop-Location
            }
        }

        $TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("sbox-panel-push-" + [guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Path $TempDir -Force | Out-Null
        $Binary = Join-Path $TempDir "sbox-panel-linux-$Arch"

        Write-Step "Cross-compiling linux/$Arch static binary..."
        $OrigCgo = $env:CGO_ENABLED
        $OrigGoos = $env:GOOS
        $OrigGoarch = $env:GOARCH
        Push-Location $RepoRoot
        try {
            $env:CGO_ENABLED = "0"
            $env:GOOS = "linux"
            $env:GOARCH = $Arch
            & $GoExe build -trimpath "-ldflags=-s -w -X main.Version=$Version" -o $Binary ./cmd/sbox-panel
            if ($LASTEXITCODE -ne 0) { throw "go build failed." }
        } finally {
            $env:CGO_ENABLED = $OrigCgo
            $env:GOOS = $OrigGoos
            $env:GOARCH = $OrigGoarch
            Pop-Location
        }
    } else {
        if (-not $Binary -or -not (Test-Path $Binary)) {
            throw "-SkipBuild requires an existing file via -Binary <path>."
        }
        $Binary = (Resolve-Path $Binary).Path
    }

    Write-Step "Preparing $RemoteDir on $HostAlias..."
    & ssh -o ConnectTimeout=10 $HostAlias "mkdir -p '$RemoteDir/data'"
    if ($LASTEXITCODE -ne 0) { throw "Failed to create remote directory '$RemoteDir/data'." }

    Write-Step "Uploading binary to ${HostAlias}:${RemoteDir}/sbox-panel.new..."
    & scp $Binary "${HostAlias}:${RemoteDir}/sbox-panel.new"
    if ($LASTEXITCODE -ne 0) { throw "scp upload failed." }

    $UseSystemdFlag = if ($NoSystemd) { "no" } else { "auto" }

    $RemoteScript = @'
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
'@

    # Ensure LF line endings and strip any pipeline CR on the remote side before executing
    $RemoteScriptLF = $RemoteScript -replace "`r`n", "`n"

    Write-Step "Deploying and restarting test instance on $HostAlias..."
    $RemoteCmd = "tr -d '\r' | REMOTE_DIR='$RemoteDir' LISTEN_ADDR='$ListenAddr' USE_SYSTEMD='$UseSystemdFlag' bash -s"
    $RemoteScriptLF | & ssh $HostAlias $RemoteCmd
    if ($LASTEXITCODE -ne 0) { throw "Remote deployment command failed." }

    Write-Step "Done."
} finally {
    if ($TempDir -and (Test-Path $TempDir)) {
        Remove-Item -Path $TempDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}
