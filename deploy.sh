#!/usr/bin/env bash
set -Eeuo pipefail

REPO_URL="${REPO_URL:-https://github.com/alert0/vpngate-to-socks.git}"
REPO_REF="${REPO_REF:-main}"
INSTALL_ROOT="${INSTALL_ROOT:-/opt/vpngate}"
SRC_DIR="${SRC_DIR:-${INSTALL_ROOT}/src}"
BIN_DIR="${BIN_DIR:-${INSTALL_ROOT}/bin}"
CONFIG_DIR="${CONFIG_DIR:-/etc/vpngate}"
RUNNER_ENV="${RUNNER_ENV:-${CONFIG_DIR}/runner.env}"
WEB_ENV="${WEB_ENV:-${CONFIG_DIR}/web.env}"
SOCKS_CONFIG_FILE="${SOCKS_CONFIG_FILE:-${CONFIG_DIR}/socks.json}"
WEB_USER="${WEB_USER:-vpngate}"
WEB_GROUP="${WEB_GROUP:-vpngate}"
WEB_LISTEN_ADDR="${WEB_LISTEN_ADDR:-0.0.0.0:5777}"
WEB_USERNAME="${WEB_USERNAME:-admin}"
WEB_SESSION_TTL="${WEB_SESSION_TTL:-12h}"
RUNNER_CONTROL_ADDR="${RUNNER_CONTROL_ADDR:-127.0.0.1:18081}"
SOCKS_LISTEN_ADDR="${SOCKS_LISTEN_ADDR:-0.0.0.0:5888}"
SOCKS_USERNAME="${SOCKS_USERNAME:-proxy}"
OPEN_FIREWALL="${OPEN_FIREWALL:-1}"
FORCE_CONFIG="${FORCE_CONFIG:-0}"

WEB_PASSWORD="${WEB_PASSWORD:-}"
SOCKS_PASSWORD="${SOCKS_PASSWORD:-}"
WEB_TLS_CERT="${WEB_TLS_CERT:-}"
WEB_TLS_KEY="${WEB_TLS_KEY:-}"

ACTIVE_RUNNER_CONTROL_ADDR="$RUNNER_CONTROL_ADDR"
ACTIVE_WEB_LISTEN_ADDR="$WEB_LISTEN_ADDR"
ACTIVE_WEB_TLS_CERT="$WEB_TLS_CERT"
ACTIVE_SOCKS_LISTEN_ADDR="$SOCKS_LISTEN_ADDR"

log() { printf '\033[1;34m[VPNGate]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[VPNGate WARN]\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31m[VPNGate ERROR]\033[0m %s\n' "$*" >&2; exit 1; }

on_error() {
  local code=$?
  warn "部署失败，退出码：${code}"
  if command -v systemctl >/dev/null 2>&1; then
    systemctl --no-pager --full status vpngate-runner.service 2>/dev/null || true
    systemctl --no-pager --full status vpngate-web.service 2>/dev/null || true
  fi
  exit "$code"
}
trap on_error ERR

require_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    die "请使用 root 运行，例如：sudo -E bash deploy.sh"
  fi
  command -v systemctl >/dev/null 2>&1 || die "当前系统没有 systemd，本脚本仅支持 systemd Linux。"
}

random_hex() {
  od -An -N24 -tx1 /dev/urandom | tr -d ' \n'
}

read_env_value() {
  local file="$1" key="$2" fallback="$3" value
  if [[ ! -f "$file" ]]; then
    printf '%s' "$fallback"
    return 0
  fi

  value="$(awk -v wanted="$key" '
    index($0, wanted "=") == 1 {
      sub(/^[^=]*=/, "")
      print
      exit
    }
  ' "$file" 2>/dev/null || true)"

  if [[ -z "$value" ]]; then
    printf '%s' "$fallback"
    return 0
  fi

  if [[ "$value" == \"*\" && "$value" == *\" ]]; then
    value="${value#\"}"
    value="${value%\"}"
  elif [[ "$value" == \'*\' && "$value" == *\' ]]; then
    value="${value#\'}"
    value="${value%\'}"
  fi
  printf '%s' "$value"
}

install_packages() {
  log "安装系统依赖：OpenVPN、Git、curl、iproute2..."
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -y
    apt-get install -y ca-certificates curl git openvpn iproute2
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y ca-certificates curl git openvpn iproute
  elif command -v yum >/dev/null 2>&1; then
    yum install -y ca-certificates curl git openvpn iproute
  else
    die "暂不支持当前发行版的包管理器。支持 apt、dnf、yum。"
  fi

  command -v openvpn >/dev/null 2>&1 || die "OpenVPN 安装失败。"
  command -v git >/dev/null 2>&1 || die "Git 安装失败。"
  command -v curl >/dev/null 2>&1 || die "curl 安装失败。"
  command -v ip >/dev/null 2>&1 || die "iproute2/iproute 安装失败。"
}

sync_source() {
  mkdir -p "$INSTALL_ROOT"
  if [[ -d "${SRC_DIR}/.git" ]]; then
    log "更新项目源码：${REPO_REF}"
    git -C "$SRC_DIR" fetch --prune origin
    git -C "$SRC_DIR" checkout -f "$REPO_REF" 2>/dev/null || git -C "$SRC_DIR" checkout -f -B "$REPO_REF" "origin/${REPO_REF}"
    git -C "$SRC_DIR" reset --hard "origin/${REPO_REF}"
  else
    log "下载项目源码：${REPO_URL} (${REPO_REF})"
    rm -rf "$SRC_DIR"
    git clone --depth 1 --branch "$REPO_REF" "$REPO_URL" "$SRC_DIR"
  fi
}

map_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) die "暂不支持 CPU 架构：$(uname -m)。目前支持 amd64、arm64。" ;;
  esac
}

install_private_go() {
  local go_version arch go_root tmp_dir archive
  go_version="$(awk '/^go[[:space:]]+[0-9]+\./ {print $2; exit}' "${SRC_DIR}/go.mod")"
  [[ -n "$go_version" ]] || die "无法从 go.mod 读取 Go 版本。"
  arch="$(map_arch)"
  go_root="${INSTALL_ROOT}/go/go${go_version}"

  if [[ ! -x "${go_root}/bin/go" ]]; then
    log "安装项目专用 Go ${go_version} (${arch})"
    tmp_dir="$(mktemp -d)"
    archive="${tmp_dir}/go.tar.gz"
    curl -fL --retry 3 --connect-timeout 15 \
      "https://go.dev/dl/go${go_version}.linux-${arch}.tar.gz" \
      -o "$archive"
    tar -C "$tmp_dir" -xzf "$archive"
    mkdir -p "$(dirname "$go_root")"
    rm -rf "$go_root"
    mv "${tmp_dir}/go" "$go_root"
    rm -rf "$tmp_dir"
  fi

  GO_BIN="${go_root}/bin/go"
  export GO_BIN
  log "使用：$($GO_BIN version)"
}

create_service_user() {
  if ! getent group "$WEB_GROUP" >/dev/null 2>&1; then
    groupadd --system "$WEB_GROUP"
  fi
  if ! id "$WEB_USER" >/dev/null 2>&1; then
    useradd --system --gid "$WEB_GROUP" --home-dir /var/lib/vpngate --create-home --shell /usr/sbin/nologin "$WEB_USER"
  fi
}

write_initial_config() {
  mkdir -p "$CONFIG_DIR"
  chmod 750 "$CONFIG_DIR"

  if [[ -n "$WEB_TLS_CERT" || -n "$WEB_TLS_KEY" ]]; then
    [[ -n "$WEB_TLS_CERT" && -n "$WEB_TLS_KEY" ]] || die "WEB_TLS_CERT 与 WEB_TLS_KEY 必须同时设置。"
    [[ -r "$WEB_TLS_CERT" ]] || die "无法读取 WEB_TLS_CERT：${WEB_TLS_CERT}"
    [[ -r "$WEB_TLS_KEY" ]] || die "无法读取 WEB_TLS_KEY：${WEB_TLS_KEY}"
  fi

  if [[ "$FORCE_CONFIG" == "1" || ! -f "$RUNNER_ENV" ]]; then
    [[ -n "$SOCKS_PASSWORD" ]] || SOCKS_PASSWORD="$(random_hex)"
    cat >"$RUNNER_ENV" <<EOF
RUNNER_CONTROL_ADDR=${RUNNER_CONTROL_ADDR}
SOCKS_CONFIG_FILE=${SOCKS_CONFIG_FILE}
SOCKS_LISTEN_ADDR=${SOCKS_LISTEN_ADDR}
SOCKS_USERNAME=${SOCKS_USERNAME}
SOCKS_PASSWORD=${SOCKS_PASSWORD}
AUTO_CONNECT=true
EOF
    chmod 600 "$RUNNER_ENV"
    RUNNER_CONFIG_CREATED=1
  else
    RUNNER_CONFIG_CREATED=0
    log "保留现有 Runner 配置：${RUNNER_ENV}"
  fi

  if [[ "$FORCE_CONFIG" == "1" || ! -f "$WEB_ENV" ]]; then
    [[ -n "$WEB_PASSWORD" ]] || WEB_PASSWORD="$(random_hex)"
    cat >"$WEB_ENV" <<EOF
WEB_LISTEN_ADDR=${WEB_LISTEN_ADDR}
WEB_USERNAME=${WEB_USERNAME}
WEB_PASSWORD=${WEB_PASSWORD}
WEB_SESSION_TTL=${WEB_SESSION_TTL}
RUNNER_API_URL=http://${RUNNER_CONTROL_ADDR}
EOF
    if [[ -n "$WEB_TLS_CERT" ]]; then
      printf 'WEB_TLS_CERT=%s\nWEB_TLS_KEY=%s\n' "$WEB_TLS_CERT" "$WEB_TLS_KEY" >>"$WEB_ENV"
    fi
    chown root:"$WEB_GROUP" "$WEB_ENV"
    chmod 640 "$WEB_ENV"
    WEB_CONFIG_CREATED=1
  else
    WEB_CONFIG_CREATED=0
    log "保留现有 Web 配置：${WEB_ENV}"
  fi

  # Persisted SOCKS settings are created by Runner only after an admin saves settings.
  # If the file already exists, keep it untouched so Web-managed credentials survive upgrades.
  if [[ -f "$SOCKS_CONFIG_FILE" ]]; then
    chmod 600 "$SOCKS_CONFIG_FILE" || true
  fi
}

refresh_active_config() {
  ACTIVE_RUNNER_CONTROL_ADDR="$(read_env_value "$RUNNER_ENV" RUNNER_CONTROL_ADDR "$RUNNER_CONTROL_ADDR")"
  ACTIVE_WEB_LISTEN_ADDR="$(read_env_value "$WEB_ENV" WEB_LISTEN_ADDR "$WEB_LISTEN_ADDR")"
  ACTIVE_WEB_TLS_CERT="$(read_env_value "$WEB_ENV" WEB_TLS_CERT "$WEB_TLS_CERT")"
  ACTIVE_SOCKS_LISTEN_ADDR="$(read_env_value "$RUNNER_ENV" SOCKS_LISTEN_ADDR "$SOCKS_LISTEN_ADDR")"
}

refresh_active_socks_from_runner() {
  local payload addr
  payload="$(curl -fsS --max-time 3 "http://${ACTIVE_RUNNER_CONTROL_ADDR}/socks/config" 2>/dev/null || true)"
  addr="$(printf '%s' "$payload" | sed -n 's/.*"listenAddr":"\([^"]*\)".*/\1/p')"
  if [[ -n "$addr" ]]; then
    ACTIVE_SOCKS_LISTEN_ADDR="$addr"
  fi
}

build_binaries() {
  log "编译 Web 与 Runner"
  mkdir -p "$BIN_DIR"
  local build_dir
  build_dir="$(mktemp -d)"
  (
    cd "$SRC_DIR"
    CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags='-s -w' -o "${build_dir}/vpngate-web" .
    CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags='-s -w' -o "${build_dir}/vpngate-runner" ./cmd/vpngate-runner
  )
  install -m 0755 "${build_dir}/vpngate-web" "${BIN_DIR}/vpngate-web"
  install -m 0755 "${build_dir}/vpngate-runner" "${BIN_DIR}/vpngate-runner"
  rm -rf "$build_dir"
}

install_systemd_units() {
  log "安装 systemd 服务"
  cat >/etc/systemd/system/vpngate-runner.service <<EOF
[Unit]
Description=VPNGate Runner and SOCKS5 Proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
EnvironmentFile=${RUNNER_ENV}
ExecStart=${BIN_DIR}/vpngate-runner
Restart=on-failure
RestartSec=3
TimeoutStopSec=25
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

  cat >/etc/systemd/system/vpngate-web.service <<EOF
[Unit]
Description=VPNGate Web Admin
After=network-online.target vpngate-runner.service
Wants=network-online.target
Requires=vpngate-runner.service

[Service]
Type=simple
User=${WEB_USER}
Group=${WEB_GROUP}
EnvironmentFile=${WEB_ENV}
ExecStart=${BIN_DIR}/vpngate-web
Restart=on-failure
RestartSec=3
TimeoutStopSec=20
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable vpngate-runner.service vpngate-web.service >/dev/null
  systemctl restart vpngate-runner.service
  systemctl restart vpngate-web.service
}

wait_for_health() {
  log "检查服务状态"
  local i web_local_port web_health_url curl_tls_args=()

  for i in $(seq 1 20); do
    if curl -fsS --max-time 2 "http://${ACTIVE_RUNNER_CONTROL_ADDR}/health" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  curl -fsS --max-time 3 "http://${ACTIVE_RUNNER_CONTROL_ADDR}/health" >/dev/null || die "Runner 健康检查失败，请查看 journalctl -u vpngate-runner。"

  web_local_port="${ACTIVE_WEB_LISTEN_ADDR##*:}"
  if [[ -n "$ACTIVE_WEB_TLS_CERT" ]]; then
    web_health_url="https://127.0.0.1:${web_local_port}/health"
    curl_tls_args=(-k)
  else
    web_health_url="http://127.0.0.1:${web_local_port}/health"
  fi

  for i in $(seq 1 20); do
    if curl "${curl_tls_args[@]}" -fsS --max-time 2 "$web_health_url" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  curl "${curl_tls_args[@]}" -fsS --max-time 3 "$web_health_url" >/dev/null || die "Web 健康检查失败，请查看 journalctl -u vpngate-web。"
}

open_firewall_if_active() {
  [[ "$OPEN_FIREWALL" == "1" ]] || return 0
  local web_port socks_port
  web_port="${ACTIVE_WEB_LISTEN_ADDR##*:}"
  socks_port="${ACTIVE_SOCKS_LISTEN_ADDR##*:}"

  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q '^Status: active'; then
    log "UFW 已启用，开放 TCP ${web_port} 和 ${socks_port}"
    ufw allow "${web_port}/tcp" >/dev/null
    ufw allow "${socks_port}/tcp" >/dev/null
  elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    log "firewalld 已启用，开放 TCP ${web_port} 和 ${socks_port}"
    firewall-cmd --permanent --add-port="${web_port}/tcp" >/dev/null
    firewall-cmd --permanent --add-port="${socks_port}/tcp" >/dev/null
    firewall-cmd --reload >/dev/null
  fi
}

print_summary() {
  local public_ip web_port socks_port scheme
  public_ip="$(curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)"
  [[ -n "$public_ip" ]] || public_ip="<服务器公网IP>"
  web_port="${ACTIVE_WEB_LISTEN_ADDR##*:}"
  socks_port="${ACTIVE_SOCKS_LISTEN_ADDR##*:}"
  scheme="http"
  [[ -n "$ACTIVE_WEB_TLS_CERT" ]] && scheme="https"

  printf '\n'
  log "部署完成"
  printf '%s\n' "------------------------------------------------------------"
  printf 'Web 后台: %s://%s:%s\n' "$scheme" "$public_ip" "$web_port"
  printf 'SOCKS 设置: %s://%s:%s/settings/socks\n' "$scheme" "$public_ip" "$web_port"
  printf 'SOCKS5: %s:%s\n' "$public_ip" "$socks_port"
  printf '%s\n' "------------------------------------------------------------"

  if [[ "$WEB_CONFIG_CREATED" == "1" ]]; then
    printf 'Web 用户名: %s\n' "$WEB_USERNAME"
    printf 'Web 密码:   %s\n' "$WEB_PASSWORD"
  else
    printf 'Web 登录信息: 已保留原配置 %s\n' "$WEB_ENV"
  fi

  if [[ "$RUNNER_CONFIG_CREATED" == "1" && ! -f "$SOCKS_CONFIG_FILE" ]]; then
    printf 'SOCKS 用户名: %s\n' "$SOCKS_USERNAME"
    printf 'SOCKS 密码:   %s\n' "$SOCKS_PASSWORD"
  else
    printf 'SOCKS 登录信息: 已保留原配置/后台持久化设置\n'
  fi

  printf '%s\n' "------------------------------------------------------------"
  printf '查看状态: systemctl status vpngate-runner vpngate-web\n'
  printf '查看日志: journalctl -u vpngate-runner -u vpngate-web -f\n'
  printf '重新部署/升级: 再次运行同一个 deploy.sh 即可\n'
  printf '\n'
  warn "如果云厂商有安全组/防火墙，请另外放行 TCP ${web_port} 和 ${socks_port}。"
  warn "以后如果在 Web 后台修改 SOCKS 端口，也要同步修改服务器/云安全组的放行端口。"
  if [[ -z "$ACTIVE_WEB_TLS_CERT" ]]; then
    warn "当前 Web 使用 HTTP。公网登录密码和 Session 不具备传输层加密；如有证书，可通过 WEB_TLS_CERT/WEB_TLS_KEY 启用 Go 原生 HTTPS。"
  fi
}

main() {
  require_root
  install_packages
  sync_source
  install_private_go
  create_service_user
  write_initial_config
  refresh_active_config
  build_binaries
  install_systemd_units
  wait_for_health
  refresh_active_socks_from_runner
  open_firewall_if_active

  if [[ ! -c /dev/net/tun ]]; then
    warn "未检测到 /dev/net/tun；OpenVPN 连接可能无法工作，请确认 VPS/宿主机已启用 TUN/TAP。"
  fi

  print_summary
}

main "$@"