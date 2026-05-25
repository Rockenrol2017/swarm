#!/bin/bash
# S.W.A.R.M. Install Script — Этап 1
# Устанавливает xray, tun2socks, собирает и запускает swarm-core
#
# Требования:
#   - Debian 12 / Ubuntu 22.04+
#   - root права
#   - Интернет соединение (до установки)
#
# Использование:
#   sudo bash install.sh

set -e  # Выходить при любой ошибке

# Цвета для вывода
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log()  { echo -e "${BLUE}[swarm]${NC} $1"; }
ok()   { echo -e "${GREEN}[✓]${NC} $1"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; }
err()  { echo -e "${RED}[✗]${NC} $1"; exit 1; }

# ─── Проверка root ──────────────────────────────────────────────────────────
[[ $EUID -ne 0 ]] && err "Запусти от root: sudo bash install.sh"

log "S.W.A.R.M. Установка — Этап 1"
echo ""

# ─── КРИТИЧНО: Сохранить шлюз ДО любых изменений ──────────────────────────
# Без этого после включения kill switch потеряем маршрут к VDS
DEFAULT_GW=$(ip route | grep '^default' | awk '{print $3}' | head -1)
DEFAULT_IFACE=$(ip route | grep '^default' | awk '{print $5}' | head -1)

if [[ -z "$DEFAULT_GW" || -z "$DEFAULT_IFACE" ]]; then
    err "Не удалось определить шлюз по умолчанию. Проверь интернет-соединение."
fi

log "Шлюз: $DEFAULT_GW через интерфейс $DEFAULT_IFACE"
ok "Шлюз сохранён"

# ─── Зависимости ───────────────────────────────────────────────────────────
log "Устанавливаем зависимости..."
apt-get update -q
apt-get install -y -q \
    curl wget iptables iproute2 unzip \
    ca-certificates gnupg lsb-release
ok "Зависимости установлены"

# ─── Включить ip_forward ────────────────────────────────────────────────────
log "Включаем ip_forward..."
echo 1 > /proc/sys/net/ipv4/ip_forward
if ! grep -q 'net.ipv4.ip_forward=1' /etc/sysctl.conf; then
    echo "net.ipv4.ip_forward=1" >> /etc/sysctl.conf
fi
ok "ip_forward включён"

# ─── Установить xray ────────────────────────────────────────────────────────
log "Устанавливаем xray..."
if command -v xray &>/dev/null; then
    warn "xray уже установлен: $(xray version | head -1)"
else
    bash <(curl -sL https://github.com/XTLS/Xray-install/raw/main/install-release.sh)
    ok "xray установлен"
fi

# ─── Установить tun2socks ────────────────────────────────────────────────────
log "Устанавливаем tun2socks..."
if command -v tun2socks &>/dev/null; then
    warn "tun2socks уже установлен"
else
    T2S_URL="https://github.com/xjasonlyu/tun2socks/releases/latest/download/tun2socks-linux-amd64.zip"
    wget -q "$T2S_URL" -O /tmp/t2s.zip
    unzip -q /tmp/t2s.zip -d /tmp/
    mv /tmp/tun2socks-linux-amd64 /usr/local/bin/tun2socks
    chmod +x /usr/local/bin/tun2socks
    rm -f /tmp/t2s.zip
    ok "tun2socks установлен"
fi

# ─── Установить Go ──────────────────────────────────────────────────────────
if ! command -v go &>/dev/null; then
    log "Устанавливаем Go 1.22..."
    GO_VERSION="1.22.4"
    wget -q "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -O /tmp/go.tar.gz
    rm -rf /usr/local/go
    tar -C /usr/local -xzf /tmp/go.tar.gz
    rm -f /tmp/go.tar.gz
    export PATH=$PATH:/usr/local/go/bin
    echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile.d/go.sh
    ok "Go ${GO_VERSION} установлен"
else
    ok "Go уже установлен: $(go version)"
fi

export PATH=$PATH:/usr/local/go/bin

# ─── Запросить данные подключения ────────────────────────────────────────────
echo ""
log "Введи данные VLESS + Reality подключения:"
echo ""
read -rp "  UUID VLESS:            " VLESS_UUID
read -rp "  IP VDS:                " VDS_IP
read -rp "  Порт VDS:              " VDS_PORT
read -rp "  SNI (напр. www.microsoft.com): " VDS_SNI
read -rp "  PublicKey Reality:     " PUBLIC_KEY
read -rp "  ShortId Reality:       " SHORT_ID
echo ""

# Базовая валидация
[[ -z "$VLESS_UUID" ]] && err "UUID не может быть пустым"
[[ -z "$VDS_IP" ]]     && err "IP не может быть пустым"
[[ -z "$VDS_PORT" ]]   && err "Порт не может быть пустым"
[[ -z "$PUBLIC_KEY" ]] && err "PublicKey не может быть пустым"

# ─── Создать конфиг ──────────────────────────────────────────────────────────
log "Создаём /etc/swarm/config.json..."
mkdir -p /etc/swarm

cat > /etc/swarm/config.json << EOF
{
  "vless_uuid":     "$VLESS_UUID",
  "vless_server":   "$VDS_IP:$VDS_PORT",
  "vds_ip":         "$VDS_IP",
  "vds_port":       $VDS_PORT,
  "sni":            "$VDS_SNI",
  "public_key":     "$PUBLIC_KEY",
  "short_id":       "$SHORT_ID",
  "tun_name":       "swarm0",
  "tun_ip":         "10.0.0.1",
  "socks5_port":    1080,
  "web_port":       8080,
  "default_gw":     "$DEFAULT_GW",
  "default_iface":  "$DEFAULT_IFACE"
}
EOF
chmod 600 /etc/swarm/config.json
ok "Конфиг создан (права 600)"

# ─── Скопировать шаблон xray ─────────────────────────────────────────────────
cp "$(dirname "$0")/../configs/xray-template.json" /etc/swarm/xray-template.json
chmod 600 /etc/swarm/xray-template.json
ok "Шаблон xray скопирован"

# ─── Установить веб-файлы ─────────────────────────────────────────────────────
mkdir -p /usr/share/swarm
cp "$(dirname "$0")/../web/index.html" /usr/share/swarm/index.html
ok "Веб-интерфейс установлен"

# ─── Собрать swarm-core ───────────────────────────────────────────────────────
log "Компилируем swarm-core..."
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$SRC_DIR"
go build -ldflags="-s -w" -o /usr/local/bin/swarm-core ./cmd/swarm-core/
chmod +x /usr/local/bin/swarm-core
ok "swarm-core скомпилирован"

# ─── systemd сервис ───────────────────────────────────────────────────────────
log "Устанавливаем systemd сервис..."
cp "$SCRIPT_DIR/systemd/swarm-core.service" /etc/systemd/system/
systemctl daemon-reload
systemctl enable swarm-core
ok "Сервис зарегистрирован"

# ─── Запуск ───────────────────────────────────────────────────────────────────
log "Запускаем S.W.A.R.M...."
systemctl start swarm-core

# Небольшая пауза для старта
sleep 3

if systemctl is-active --quiet swarm-core; then
    ok "S.W.A.R.M. запущен!"
else
    err "Сервис не запустился. Проверь: journalctl -u swarm-core -n 50"
fi

echo ""
echo -e "${GREEN}╔══════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║       S.W.A.R.M. УСТАНОВЛЕН! 🚀          ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════╝${NC}"
echo ""
LOCAL_IP=$(hostname -I | awk '{print $1}')
echo -e "  Веб-дашборд:   ${BLUE}http://${LOCAL_IP}:8080${NC}"
echo -e "  Логи:          journalctl -u swarm-core -f"
echo -e "  Статус:        systemctl status swarm-core"
echo ""
echo -e "  Проверить IP:  curl https://api.ipinfo.io"
echo -e "  Проверить DNS: nslookup google.com"
echo ""
echo -e "${YELLOW}  Следующий шаг: настрой шлюз на роутере → ${LOCAL_IP}${NC}"
