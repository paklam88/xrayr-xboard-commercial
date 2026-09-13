#!/usr/bin/env bash
# One-click install for paklam88/xrayr-xboard-commercial
# Usage examples:
#   PANEL_URL=https://panel.example.com PANEL_KEY=token ./install.sh --node-id 1
#   ./install.sh --panel-url https://panel.example.com --panel-key token --node-id 1
#   ./install.sh 1   # interactive prompts for PANEL_URL / PANEL_KEY if unset

set -euo pipefail

REPO="paklam88/xrayr-xboard-commercial"
INSTALL_DIR="/opt/xrayr"
SERVICE_NAME="xrayr"
BINARY_NAME="XrayR"
CONFIG_PATH="${INSTALL_DIR}/config.yml"
SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

# -------- Configurable variables (override via env or flags) --------
PANEL_URL="${PANEL_URL:-}"
PANEL_KEY="${PANEL_KEY:-}"
NODE_ID="${NODE_ID:-}"
NODE_TYPE="${NODE_TYPE:-V2ray}"
PANEL_TYPE="${PANEL_TYPE:-XBoard}"
UPDATE_PERIODIC="${UPDATE_PERIODIC:-15}"
ENABLE_AUDIT="${ENABLE_AUDIT:-true}"
ENABLE_METRICS="${ENABLE_METRICS:-false}"
METRICS_LISTEN="${METRICS_LISTEN:-0.0.0.0:9091}"

red() { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
die() { red "ERROR: $*"; exit 1; }

need_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    die "Please run as root (sudo)."
  fi
}

usage() {
  cat <<'EOF'
XrayR XBoard commercial installer

Options:
  --panel-url URL       Panel base URL (or env PANEL_URL)
  --panel-key KEY       Server token / ApiKey (or env PANEL_KEY)
  --node-id ID          Node ID (or env NODE_ID / positional arg)
  --node-type TYPE      V2ray|Vmess|Vless|Trojan|Shadowsocks (default: V2ray)
  --panel-type TYPE     XBoard|NewV2board|V2board (default: XBoard)
  --no-audit            Disable local BT/SMTP/private audit routing
  --enable-metrics      Enable Prometheus metrics on 0.0.0.0:9091
  -h, --help            Show this help

Examples:
  PANEL_URL=https://panel.example.com PANEL_KEY=xxx ./install.sh --node-id 1
  ./install.sh --panel-url https://panel.example.com --panel-key xxx 1
EOF
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --panel-url)
        PANEL_URL="${2:-}"; shift 2 ;;
      --panel-key)
        PANEL_KEY="${2:-}"; shift 2 ;;
      --node-id)
        NODE_ID="${2:-}"; shift 2 ;;
      --node-type)
        NODE_TYPE="${2:-}"; shift 2 ;;
      --panel-type)
        PANEL_TYPE="${2:-}"; shift 2 ;;
      --no-audit)
        ENABLE_AUDIT="false"; shift ;;
      --enable-metrics)
        ENABLE_METRICS="true"; shift ;;
      -h|--help)
        usage; exit 0 ;;
      --)
        shift; break ;;
      -*)
        die "Unknown option: $1" ;;
      *)
        # positional NODE_ID
        if [[ -z "${NODE_ID}" ]]; then
          NODE_ID="$1"
        else
          die "Unexpected argument: $1"
        fi
        shift ;;
    esac
  done
}

prompt_if_empty() {
  local var_name="$1"
  local prompt="$2"
  local silent="${3:-0}"
  local current="${!var_name:-}"
  if [[ -n "${current}" ]]; then
    return 0
  fi
  if [[ ! -t 0 ]]; then
    die "${var_name} is required (non-interactive mode)."
  fi
  if [[ "${silent}" == "1" ]]; then
    read -r -s -p "${prompt}: " current
    echo
  else
    read -r -p "${prompt}: " current
  fi
  printf -v "${var_name}" '%s' "${current}"
}

detect_arch_asset() {
  local arch
  arch="$(uname -m)"
  case "${arch}" in
    x86_64|amd64)
      ASSET_NAME="XrayR-linux-64.tar.gz"
      ARCH_LABEL="x86_64"
      ;;
    aarch64|arm64)
      ASSET_NAME="XrayR-linux-arm64.tar.gz"
      ARCH_LABEL="aarch64"
      ;;
    *)
      die "Unsupported architecture: ${arch} (need x86_64 or aarch64)"
      ;;
  esac
}

require_cmds() {
  local missing=()
  for c in curl tar systemctl; do
    command -v "$c" >/dev/null 2>&1 || missing+=("$c")
  done
  if [[ ${#missing[@]} -gt 0 ]]; then
    die "Missing required commands: ${missing[*]}"
  fi
}

download_latest_release() {
  local api="https://api.github.com/repos/${REPO}/releases/latest"
  yellow "Fetching latest release metadata from ${REPO} ..."
  local json
  if ! json="$(curl -fsSL -H 'Accept: application/vnd.github+json' "${api}")"; then
    die "Failed to query GitHub Releases API (${api}). Publish a release first."
  fi

  local tag
  tag="$(printf '%s' "${json}" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  [[ -n "${tag}" ]] || die "Cannot parse release tag_name."

  local url
  url="$(printf '%s' "${json}" | tr '\n' ' ' | sed -n "s/.*\"browser_download_url\"[[:space:]]*:[[:space:]]*\"\\([^\"]*${ASSET_NAME}\\)\".*/\\1/p" | head -n1)"
  if [[ -z "${url}" ]]; then
    # Fallback: construct conventional asset URL
    url="https://github.com/${REPO}/releases/download/${tag}/${ASSET_NAME}"
  fi

  green "Release: ${tag} | Arch: ${ARCH_LABEL} | Asset: ${ASSET_NAME}"
  TMP_DIR="$(mktemp -d)"
  trap 'rm -rf "${TMP_DIR}"' EXIT

  yellow "Downloading ${url}"
  curl -fL --retry 3 --retry-delay 2 -o "${TMP_DIR}/${ASSET_NAME}" "${url}" \
    || die "Download failed. Ensure release asset ${ASSET_NAME} exists for tag ${tag}."

  yellow "Extracting to ${INSTALL_DIR}"
  mkdir -p "${INSTALL_DIR}"
  tar -xzf "${TMP_DIR}/${ASSET_NAME}" -C "${INSTALL_DIR}"
  chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
  [[ -x "${INSTALL_DIR}/${BINARY_NAME}" ]] || die "Binary ${INSTALL_DIR}/${BINARY_NAME} missing after extract."
  [[ -f "${INSTALL_DIR}/geoip.dat" ]] || yellow "WARN: geoip.dat not found in package"
  [[ -f "${INSTALL_DIR}/geosite.dat" ]] || yellow "WARN: geosite.dat not found in package"
}

write_config() {
  if [[ -f "${CONFIG_PATH}" ]]; then
    local bak="${CONFIG_PATH}.bak.$(date +%Y%m%d%H%M%S)"
    yellow "Existing config found; backing up to ${bak}"
    cp -a "${CONFIG_PATH}" "${bak}"
  fi

  # Strip trailing slash from panel URL
  local host="${PANEL_URL%/}"

  cat > "${CONFIG_PATH}" <<EOF
Log:
  Level: warning
  AccessPath: # ${INSTALL_DIR}/access.log
  ErrorPath: # ${INSTALL_DIR}/error.log
DnsConfigPath: # ${INSTALL_DIR}/dns.json
RouteConfigPath: # ${INSTALL_DIR}/route.json
InboundConfigPath: # ${INSTALL_DIR}/custom_inbound.json
OutboundConfigPath: # ${INSTALL_DIR}/custom_outbound.json
ConnectionConfig:
  Handshake: 4
  ConnIdle: 30
  UplinkOnly: 2
  DownlinkOnly: 4
  BufferSize: 64
Metrics:
  Enable: ${ENABLE_METRICS}
  Listen: "${METRICS_LISTEN}"
Nodes:
  - PanelType: "${PANEL_TYPE}"
    ApiConfig:
      ApiHost: "${host}"
      ApiKey: "${PANEL_KEY}"
      NodeID: ${NODE_ID}
      NodeType: ${NODE_TYPE}
      Timeout: 15
      EnableVless: false
      VlessFlow: "xtls-rprx-vision"
      SpeedLimit: 0
      DeviceLimit: 0
    ControllerConfig:
      ListenIP: 0.0.0.0
      SendIP: 0.0.0.0
      UpdatePeriodic: ${UPDATE_PERIODIC}
      DeviceLimitWindow: 60
      InactiveGCHours: 24
      EnableAudit: ${ENABLE_AUDIT}
      EnableDNS: false
      DNSType: AsIs
      EnableProxyProtocol: false
      EnableFallback: false
      DisableLocalREALITYConfig: false
      EnableREALITY: false
      CertConfig:
        CertMode: none
        CertDomain: "node.example.com"
        CertFile: ${INSTALL_DIR}/cert/node.example.com.cert
        KeyFile: ${INSTALL_DIR}/cert/node.example.com.key
EOF
  chmod 600 "${CONFIG_PATH}"
  green "Wrote ${CONFIG_PATH}"
}

write_systemd() {
  cat > "${SERVICE_PATH}" <<EOF
[Unit]
Description=XrayR XBoard commercial node
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/${BINARY_NAME} -config ${CONFIG_PATH}
Restart=always
RestartSec=5
LimitNOFILE=1048576
LimitNPROC=512
Environment=XRAY_LOCATION_ASSET=${INSTALL_DIR}
Environment=XRAY_LOCATION_CONFIG=${INSTALL_DIR}

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}.service"
  systemctl restart "${SERVICE_NAME}.service"
  green "systemd unit ${SERVICE_NAME}.service enabled and started"
}

verify_service() {
  sleep 2
  echo
  yellow "==== systemctl status ${SERVICE_NAME} ===="
  systemctl --no-pager --full status "${SERVICE_NAME}.service" || true
  echo
  if systemctl is-active --quiet "${SERVICE_NAME}.service"; then
    green "OK: ${SERVICE_NAME} is active (running)"
  else
    red "WARN: ${SERVICE_NAME} is not active. Check logs:"
    yellow "  journalctl -u ${SERVICE_NAME} -n 100 --no-pager"
    exit 1
  fi
}

main() {
  parse_args "$@"
  need_root
  require_cmds
  detect_arch_asset

  prompt_if_empty PANEL_URL "Enter PANEL_URL (e.g. https://panel.example.com)"
  prompt_if_empty PANEL_KEY "Enter PANEL_KEY / server_token" 1
  prompt_if_empty NODE_ID "Enter NODE_ID"

  [[ -n "${PANEL_URL}" ]] || die "PANEL_URL is empty"
  [[ -n "${PANEL_KEY}" ]] || die "PANEL_KEY is empty"
  [[ "${NODE_ID}" =~ ^[0-9]+$ ]] || die "NODE_ID must be a positive integer"

  download_latest_release
  write_config
  write_systemd
  verify_service

  echo
  green "Install complete."
  echo "  Binary : ${INSTALL_DIR}/${BINARY_NAME}"
  echo "  Config : ${CONFIG_PATH}"
  echo "  Service: systemctl status ${SERVICE_NAME}"
  echo "  Logs   : journalctl -u ${SERVICE_NAME} -f"
}

main "$@"
