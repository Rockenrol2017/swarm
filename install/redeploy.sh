#!/bin/bash
# redeploy.sh — пересборка и деплой на оба сервера
#
# Использование:
#   HOME_SERVER=user@192.168.1.x VDS_SERVER=root@YOUR_VDS_IP bash redeploy.sh
#
# Или отредактируй переменные ниже:

set -euo pipefail

# ─── НАСТРОЙ ПОД СВОЮ СЕТЬ ────────────────────────────────────────────────
HOME_SERVER="${HOME_SERVER:-user@YOUR_HOME_SERVER_IP}"  # домашний сервер
VDS_SERVER="${VDS_SERVER:-root@YOUR_VDS_IP}"           # VDS bootstrap
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REMOTE_SRC="~/swarm-node-src"
GOBIN="~/go_sdk/bin/go"

echo "╔══════════════════════════════════════════╗"
echo "║   S.W.A.R.M. — Redeploy v2              ║"
echo "╚══════════════════════════════════════════╝"
echo ""
echo "Источник:      $SRC_DIR"
echo "Домашний сервер: $HOME_SERVER"
echo "VDS:           $VDS_SERVER"
echo ""

# ─── 1. Синхронизация исходников на домашний сервер ────────────────────────
echo "▶ [1/4] Синхронизация исходников..."
rsync -av --delete \
    --exclude='.git' \
    --exclude='*.exe' \
    --exclude='swarm-node' \
    --exclude='swarm-core' \
    "$SRC_DIR/" \
    "$HOME_SERVER:$REMOTE_SRC/"
echo "✓ Исходники обновлены"

# ─── 2. Компиляция на домашнем сервере ────────────────────────────────────
echo ""
echo "▶ [2/4] Компиляция на $HOME_SERVER..."
ssh "$HOME_SERVER" bash << 'ENDSSH'
set -euo pipefail
cd ~/swarm-node-src

# Убираем HTTP_PROXY который ломает go mod
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy

GOBIN=~/go_sdk/bin/go
if [ ! -f "$GOBIN" ]; then
    # Попробуем системный go
    GOBIN=$(which go 2>/dev/null || echo "")
fi

if [ -z "$GOBIN" ]; then
    echo "❌ Go не найден! Установите: wget -q https://go.dev/dl/go1.22.4.linux-amd64.tar.gz && tar -C ~ -xzf go1.22.4.linux-amd64.tar.gz && mv ~/go ~/go_sdk"
    exit 1
fi

echo "Go: $($GOBIN version)"
echo "Сборка..."
# GOFLAGS=-mod=mod обязателен — go.mod иногда требует tidy но мы собираем напрямую
GOFLAGS=-mod=mod $GOBIN build -trimpath -ldflags="-s -w" -o /tmp/swarm-node-new ./cmd/swarm-node/
echo "✓ Скомпилировано ($(du -sh /tmp/swarm-node-new | cut -f1))"
ENDSSH
echo "✓ Компиляция завершена"

# ─── 3. Деплой на домашний сервер (systemd) ───────────────────────────────
echo ""
echo "▶ [3/4] Деплой на домашний сервер..."
ssh "$HOME_SERVER" bash << 'ENDSSH'
set -euo pipefail

# Остановить старый процесс (любой из вариантов)
echo "→ Останавливаем старый swarm-node..."
if systemctl is-active swarm-node &>/dev/null; then
    sudo systemctl stop swarm-node
    echo "  (остановлен systemd сервис)"
elif pgrep -x swarm-node &>/dev/null; then
    pkill -x swarm-node || true
    sleep 1
    echo "  (убит процесс)"
fi

# Устанавливаем бинарник
echo "→ Устанавливаем бинарник..."
sudo mv /tmp/swarm-node-new /usr/local/bin/swarm-node
sudo chmod +x /usr/local/bin/swarm-node

# КРИТИЧНО: setcap нужно повторять после каждой замены бинарника!
# Без этого TPROXY (SO_TRANSPARENT, SO_ORIGINAL_DST) не работает.
echo "→ Устанавливаем capabilities (TPROXY)..."
sudo setcap cap_net_admin=+ep /usr/local/bin/swarm-node && \
    echo "  ✓ cap_net_admin установлен" || \
    echo "  ⚠ setcap не удался (TPROXY может не работать)"

# Создаём конфиг если нет
if [ ! -f /etc/swarm/node-config.json ]; then
    sudo mkdir -p /etc/swarm
    sudo tee /etc/swarm/node-config.json > /dev/null << 'CONF'
{
  "listen_addr": "",
  "socks5_addr": ":1090",
  "tproxy_addr": "",
  "identity_file": "/etc/swarm/identity.json",
  "bootstrap_addr": "YOUR_VDS_IP:7437",
  "bootstrap_node_id": "",
  "max_peers": 50,
  "mode": "client"
}
CONF
    echo "  ✓ Конфиг создан: /etc/swarm/node-config.json"
else
    echo "  ✓ Конфиг уже существует"
fi

# Устанавливаем TPROXY скрипты
echo "→ Устанавливаем TPROXY скрипты..."
sudo mkdir -p /usr/local/share/swarm
sudo cp ~/swarm-node-src/install/tproxy-rules.sh /usr/local/share/swarm/
sudo cp ~/swarm-node-src/install/tproxy-cleanup.sh /usr/local/share/swarm/
sudo chmod +x /usr/local/share/swarm/tproxy-rules.sh
sudo chmod +x /usr/local/share/swarm/tproxy-cleanup.sh

# Устанавливаем systemd unit
echo "→ Устанавливаем systemd unit..."
sudo cp ~/swarm-node-src/install/systemd/swarm-node.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable swarm-node

# Запускаем
echo "→ Запускаем swarm-node..."
sudo systemctl start swarm-node
sleep 2

if systemctl is-active swarm-node &>/dev/null; then
    echo "✅ swarm-node запущен через systemd!"
    echo "--- последние строки лога ---"
    sudo journalctl -u swarm-node -n 10 --no-pager
else
    echo "❌ swarm-node не запустился!"
    sudo journalctl -u swarm-node -n 20 --no-pager
    exit 1
fi
ENDSSH

# ─── 4. Деплой на VDS ─────────────────────────────────────────────────────
echo ""
echo "▶ [4/4] Деплой на VDS ($VDS_SERVER)..."

# Собираем для VDS прямо на домашнем сервере (тот же amd64)
echo "→ Копируем бинарник на VDS..."
ssh "$HOME_SERVER" "scp /usr/local/bin/swarm-node $VDS_SERVER:/tmp/swarm-node-new" 2>/dev/null || {
    # Если нет SSH ключа от home→VDS, делаем через локальную машину
    echo "  (нет ключа home→VDS, копируем напрямую с локальной)"
    scp "$HOME_SERVER:/usr/local/bin/swarm-node" /tmp/swarm-node-new
    scp /tmp/swarm-node-new "$VDS_SERVER:/tmp/swarm-node-new"
}

ssh "$VDS_SERVER" bash << 'ENDSSH'
set -euo pipefail

echo "→ Останавливаем bootstrap на VDS..."
systemctl stop swarm-node || true
sleep 1

echo "→ Обновляем бинарник..."
mv /tmp/swarm-node-new /usr/local/bin/swarm-node
chmod +x /usr/local/bin/swarm-node

echo "→ Запускаем..."
systemctl start swarm-node
sleep 2

if systemctl is-active swarm-node &>/dev/null; then
    echo "✅ Bootstrap запущен!"
    journalctl -u swarm-node -n 8 --no-pager
else
    echo "❌ Bootstrap не запустился!"
    journalctl -u swarm-node -n 20 --no-pager
    exit 1
fi
ENDSSH

echo ""
echo "╔══════════════════════════════════════════╗"
echo "║  ✅ Деплой завершён успешно!             ║"
echo "╠══════════════════════════════════════════╣"
echo "║ Домашний:  journalctl -u swarm-node -f   ║"
echo "║ VDS:       journalctl -u swarm-node -f   ║"
echo "╚══════════════════════════════════════════╝"
