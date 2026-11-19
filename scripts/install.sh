#!/usr/bin/env bash
# vps-panel agent installer.
#   bash <(curl -fsSL https://panel.example.com/install.sh) --panel https://panel.example.com --token <token> --mode probe|node
# Re-running upgrades the binary and keeps existing credentials unless --token is given.
#   --uninstall          remove the agent only (Xray keeps serving existing routes)
#   --uninstall --purge  also remove Xray, the firewall table, sysctl tuning and all agent data
set -euo pipefail

PANEL=""
TOKEN=""
MODE="probe"
SIGN_PUBKEY="__SIGN_PUBKEY__"   # filled in by the panel when serving this script
PREFIX=/usr/local/bin
CONF_DIR=/etc/vpsagent
DATA_DIR=/var/lib/vpsagent

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
step()  { printf '\033[36m==>\033[0m %s\n' "$*"; }
die()   { red "错误：$*"; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --panel) PANEL="${2%/}"; shift 2 ;;
    --token) TOKEN="$2"; shift 2 ;;
    --mode)  MODE="$2"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    --purge) PURGE=1; shift ;;
    *) die "未知参数 $1" ;;
  esac
done

[ "$(id -u)" = 0 ] || die "请用 root 运行"

uninstall() {
  step "卸载 vpsagent"
  if command -v systemctl >/dev/null 2>&1; then
    systemctl disable --now vpsagent 2>/dev/null || true
    rm -f /etc/systemd/system/vpsagent.service
    systemctl daemon-reload
  elif command -v rc-service >/dev/null 2>&1; then
    rc-service vpsagent stop 2>/dev/null || true
    rc-update del vpsagent 2>/dev/null || true
    rm -f /etc/init.d/vpsagent
  fi
  rm -f "$PREFIX/vpsagent"
  if [ "${PURGE:-0}" != 1 ]; then
    green "已卸载（保留 $CONF_DIR 和 Xray；加 --purge 可一并清除）"
    exit 0
  fi
  step "清除 Xray、防火墙规则、系统调优和 Agent 数据"
  if command -v systemctl >/dev/null 2>&1; then
    systemctl disable --now xray 2>/dev/null || true
    rm -f /etc/systemd/system/xray.service
    systemctl daemon-reload
  elif command -v rc-service >/dev/null 2>&1; then
    rc-service xray stop 2>/dev/null || true
    rc-update del xray 2>/dev/null || true
    rm -f /etc/init.d/xray
  fi
  rm -rf /usr/local/bin/xray /usr/local/etc/xray /usr/local/share/xray
  command -v nft >/dev/null 2>&1 && nft delete table inet vpspanel 2>/dev/null || true
  if [ -f /etc/sysctl.d/99-vpspanel.conf ]; then
    rm -f /etc/sysctl.d/99-vpspanel.conf
    sysctl --system >/dev/null 2>&1 || true
  fi
  rm -rf "$CONF_DIR" "$DATA_DIR"
  green "已完全清除"
  exit 0
}
[ "${UNINSTALL:-0}" = 1 ] && uninstall

[ -n "$PANEL" ] || die "缺少 --panel"
case "$MODE" in probe|node) ;; *) die "--mode 只能是 probe 或 node" ;; esac

# ---- system detection ----
step "检测系统"
. /etc/os-release 2>/dev/null || true
OS_ID="${ID:-unknown}"
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "不支持的架构 $(uname -m)" ;;
esac
echo "  系统：${PRETTY_NAME:-$OS_ID}  架构：$ARCH"

need() { command -v "$1" >/dev/null 2>&1; }
install_pkgs() {
  if need apt-get; then apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@" >/dev/null
  elif need dnf; then dnf install -y -q "$@" >/dev/null
  elif need yum; then yum install -y -q "$@" >/dev/null
  elif need apk; then apk add --no-cache -q "$@" >/dev/null
  fi
}
for bin in curl openssl; do need "$bin" || install_pkgs "$bin"; done
if [ "$MODE" = node ]; then
  need nft || install_pkgs nftables || true
  need unzip || install_pkgs unzip
fi

# ---- download + verify ----
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
step "下载 Agent"
curl -fsSL "$PANEL/dl/agent/linux-$ARCH" -o "$TMP/vpsagent" || die "下载失败"
curl -fsSL "$PANEL/dl/agent/linux-$ARCH.sha256" -o "$TMP/vpsagent.sha256" || die "下载校验文件失败"
step "校验 sha256"
echo "$(cut -d' ' -f1 "$TMP/vpsagent.sha256")  $TMP/vpsagent" | sha256sum -c - >/dev/null || die "sha256 不匹配"

if [ "$SIGN_PUBKEY" != "__SIGN_PUBKEY__" ] && [ -n "$SIGN_PUBKEY" ]; then
  step "校验 ed25519 签名"
  if curl -fsSL "$PANEL/dl/agent/linux-$ARCH.sig" -o "$TMP/vpsagent.sig.b64" 2>/dev/null; then
    base64 -d "$TMP/vpsagent.sig.b64" > "$TMP/vpsagent.sig"
    printf '%s\n' "-----BEGIN PUBLIC KEY-----" "$SIGN_PUBKEY" "-----END PUBLIC KEY-----" > "$TMP/pub.pem"
    openssl pkeyutl -verify -pubin -inkey "$TMP/pub.pem" -rawin -in "$TMP/vpsagent" -sigfile "$TMP/vpsagent.sig" >/dev/null \
      || die "签名校验失败"
  else
    die "面板配置了签名公钥，但没有找到签名文件"
  fi
fi

install -m 0755 "$TMP/vpsagent" "$PREFIX/vpsagent"
mkdir -p "$CONF_DIR" "$DATA_DIR"
chmod 700 "$CONF_DIR" "$DATA_DIR"
# Pin the release signing key locally: later self-updates must be signed by it.
if [ "$SIGN_PUBKEY" != "__SIGN_PUBKEY__" ] && [ -n "$SIGN_PUBKEY" ] && [ ! -f "$CONF_DIR/sign.pub" ]; then
  printf '%s\n' "$SIGN_PUBKEY" > "$CONF_DIR/sign.pub"
fi

# ---- local switches (only editable on the VPS) ----
if [ ! -f "$CONF_DIR/agent.yaml" ]; then
  cat > "$CONF_DIR/agent.yaml" <<EOF
# 本地安全开关：只能在 VPS 上修改，面板无法更改。修改后执行 systemctl restart vpsagent
mode: $MODE                # probe | node
allow_remote_exec: false   # 允许面板下发任意命令 / WebSSH
allow_system_tune: true    # 允许 BBR / sysctl 调优
allow_self_update: true    # 允许面板更新 Agent
EOF
else
  sed -i "s/^mode:.*/mode: $MODE                # probe | node/" "$CONF_DIR/agent.yaml"
fi

if [ -n "$TOKEN" ]; then
  step "向面板注册"
  "$PREFIX/vpsagent" register --panel "$PANEL" --token "$TOKEN" --mode "$MODE" || die "注册失败（令牌可能已过期）"
elif [ ! -f "$CONF_DIR/credentials.json" ]; then
  die "首次安装需要 --token"
fi

# ---- service ----
step "注册系统服务"
if need systemctl; then
  cat > /etc/systemd/system/vpsagent.service <<'EOF'
[Unit]
Description=vps-panel agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/vpsagent run
Restart=always
RestartSec=3
LimitNOFILE=65535
MemoryHigh=96M
MemoryMax=192M

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now vpsagent >/dev/null 2>&1
  systemctl restart vpsagent
elif need rc-service; then
  cat > /etc/init.d/vpsagent <<'EOF'
#!/sbin/openrc-run
name="vpsagent"
command="/usr/local/bin/vpsagent"
command_args="run"
command_background=true
pidfile="/run/vpsagent.pid"
output_log="/var/log/vpsagent.log"
error_log="/var/log/vpsagent.log"
depend() { need net; }
EOF
  chmod +x /etc/init.d/vpsagent
  rc-update add vpsagent default >/dev/null
  rc-service vpsagent restart
else
  die "没有找到 systemd 或 OpenRC"
fi

green "安装完成。Agent 已连接面板：$PANEL"
[ "$MODE" = node ] && echo "节点模式：面板会自动下发 Xray 安装任务。"
exit 0
