#!/bin/bash
# install.sh — установка swarm-node одной командой
#
# Использование:
#   curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install.sh | bash
#
# Что делает:
#   1. Определяет OS и архитектуру
#   2. Скачивает последний бинарник с GitHub Releases
#   3. Устанавливает в /usr/local/bin/swarm-node
#   4. Создаёт базовый конфиг (если нет)
#   5. Устанавливает systemd сервис (Linux)

set -euo pipefail

REPO="Rockenrol2017/swarm"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/swarm"
BINARY="swarm-node"

# Цвета
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()    { echo -e "${GREEN}▶${NC} $*"; }
warn()    { echo -e "${YELLOW}⚠${NC}  $*"; }
error()   { echo -e "${RED}✗${NC}  $*" >&2; exit 1; }
success() { echo -e "${GREEN}✓${NC} $*"; }

echo ""
echo "╔══════════════════════════════════════════╗"
echo "║   S.W.A.R.M. Node Installer             ║"
echo "╚══════════════════════════════════════════╝"
echo ""

# ─── Проверка root ────────────────────────────────────────────────────────────
if [ "$EUID" -ne 0 ]; then
    error "Запустите под root: sudo bash install.sh"
fi

# ─── Определяем платформу ─────────────────────────────────────────────────────
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64)  ARCH_SUFFIX="amd64" ;;
    aarch64) ARCH_SUFFIX="arm64" ;;
    armv7l)  ARCH_SUFFIX="armv7" ;;
    arm*)    ARCH_SUFFIX="armv7" ;;
    *)       error "Неподдерживаемая архитектура: $ARCH" ;;
esac

case "$OS" in
    linux)   PLATFORM="${OS}-${ARCH_SUFFIX}" ;;
    darwin)  PLATFORM="${OS}-${ARCH_SUFFIX}" ;;
    *)       error "Неподдерживаемая ОС: $OS (используйте Windows бинарник вручную)" ;;
esac

info "Платформа: $PLATFORM"

# ─── Получаем последний релиз ─────────────────────────────────────────────────
info "Получаем последнюю версию..."

LATEST=$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')

if [ -z "$LATEST" ]; then
    error "Не удалось получить версию с GitHub. Проверьте интернет-соединение."
fi

success "Последняя версия: $LATEST"

BINARY_NAME="swarm-node-${PLATFORM}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${LATEST}/${BINARY_NAME}"

# ─── Скачиваем ────────────────────────────────────────────────────────────────
info "Скачиваем $BINARY_NAME..."
TMP=$(mktemp)
curl -sSfL "$DOWNLOAD_URL" -o "$TMP" || error "Не удалось скачать: $DOWNLOAD_URL"

# Проверка что это не HTML (ошибка 404)
if file "$TMP" | grep -q "HTML"; then
    error "Бинарник не найден. Возможно, для этой платформы нет релиза."
fi

# ─── Устанавливаем ────────────────────────────────────────────────────────────
info "Устанавливаем в ${INSTALL_DIR}/${BINARY}..."
mv "$TMP" "${INSTALL_DIR}/${BINARY}"
chmod +x "${INSTALL_DIR}/${BINARY}"

# CAP_NET_ADMIN для TPROXY (если поддерживается)
if command -v setcap &>/dev/null; then
    setcap cap_net_admin=+ep "${INSTALL_DIR}/${BINARY}" 2>/dev/null && \
        success "cap_net_admin установлен (TPROXY поддерживается)" || \
        warn "setcap не удался — TPROXY недоступен (SOCKS5 работает)"
fi

success "${BINARY} ${LATEST} установлен"

# ─── Конфиг ───────────────────────────────────────────────────────────────────
mkdir -p "$CONFIG_DIR"

if [ ! -f "${CONFIG_DIR}/node-config.json" ]; then
    info "Создаём конфиг..."
    cat > "${CONFIG_DIR}/node-config.json" << 'EOF'
{
  "mode": "client",
  "socks5_addr": ":1090",
  "identity_file": "/etc/swarm/identity.json",
  "bootstrap_addr": "193.68.89.168:7437",
  "bootstrap_addrs": ["132.243.213.6:7437", "192.177.26.9:7437"],
  "max_peers": 50,
  "status_addr": ":19090"
}
EOF
    success "Конфиг создан: ${CONFIG_DIR}/node-config.json"
else
    success "Конфиг уже существует (не перезаписываем)"
fi

# ─── Systemd (только Linux) ───────────────────────────────────────────────────
if [ "$OS" = "linux" ] && command -v systemctl &>/dev/null; then
    info "Устанавливаем systemd сервис..."

    # Скачиваем service файл
    curl -sSfL "https://raw.githubusercontent.com/${REPO}/main/install/systemd/swarm-node.service" \
        -o /etc/systemd/system/swarm-node.service 2>/dev/null || \
    # Fallback: создаём минимальный
    cat > /etc/systemd/system/swarm-node.service << 'EOF'
[Unit]
Description=S.W.A.R.M. Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/swarm-node -config /etc/swarm/node-config.json
Restart=always
RestartSec=10
MemoryMax=128M

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable swarm-node
    systemctl restart swarm-node
    sleep 3

    if systemctl is-active swarm-node &>/dev/null; then
        success "swarm-node запущен!"
    else
        warn "Сервис не запустился. Логи: journalctl -u swarm-node -n 20"
    fi
fi

# ─── Итог ─────────────────────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║  ✅ S.W.A.R.M. установлен!                         ║"
echo "╠══════════════════════════════════════════════════════╣"
echo "║  Версия:  $LATEST                                    "
echo "║  Бинарник: /usr/local/bin/swarm-node                ║"
echo "║  Конфиг:  /etc/swarm/node-config.json               ║"
echo "╠══════════════════════════════════════════════════════╣"
echo "║  Статус:  curl http://localhost:19090/health         ║"
echo "║  Логи:    journalctl -u swarm-node -f                ║"
echo "║  SOCKS5:  127.0.0.1:1090                            ║"
echo "╚══════════════════════════════════════════════════════╝"
echo ""
echo "Настрой браузер/систему на SOCKS5 прокси 127.0.0.1:1090"
echo "Или отредактируй конфиг: ${CONFIG_DIR}/node-config.json"
echo ""
