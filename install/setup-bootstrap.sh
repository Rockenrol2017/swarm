#!/bin/bash
# setup-bootstrap.sh — первичная настройка bootstrap узла на новом VDS
#
# Запускать на VDS под root:
#   curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install/setup-bootstrap.sh | bash
# или:
#   bash setup-bootstrap.sh
#
# Что делает:
#   1. Устанавливает Go (если нет)
#   2. Клонирует репозиторий
#   3. Собирает swarm-node
#   4. Создаёт конфиг (mode: bootstrap)
#   5. Устанавливает systemd сервис
#   6. Открывает нужные порты (ufw/iptables)

set -euo pipefail

REPO_URL="https://github.com/Rockenrol2017/swarm"
REMOTE_SRC=~/swarm-node-src
LISTEN_PORT=7437
STATUS_PORT=19090

echo "╔══════════════════════════════════════════╗"
echo "║   S.W.A.R.M. Bootstrap Setup            ║"
echo "╚══════════════════════════════════════════╝"
echo ""

# ─── 0. Синхронизация часов ───────────────────────────────────────────────────
# Критично: handshake проверяет временну́ю метку (скос > 1м30с = отказ).
# Новые VDS часто стартуют с рассинхронизированными часами.
echo "▶ [0/5] Синхронизация времени..."
if command -v timedatectl &>/dev/null; then
    timedatectl set-ntp true 2>/dev/null || true
fi
# Принудительная синхронизация через chronyc или systemd-timesyncd
if command -v chronyc &>/dev/null; then
    chronyc makestep 2>/dev/null && echo "✓ Часы синхронизированы (chrony)" || true
elif systemctl is-active systemd-timesyncd &>/dev/null; then
    # Ждём синхронизации до 30 секунд
    for i in $(seq 1 6); do
        sleep 5
        if timedatectl show --property=NTPSynchronized --value 2>/dev/null | grep -q "yes"; then
            echo "✓ Часы синхронизированы (timesyncd)"
            break
        fi
    done
fi
CURRENT_TIME=$(date -u +"%Y-%m-%d %H:%M:%S UTC")
echo "  Текущее время: $CURRENT_TIME"

# ─── 1. Go ───────────────────────────────────────────────────────────────────
if ! command -v go &>/dev/null && [ ! -x /tmp/go/bin/go ] && [ ! -x /usr/local/go/bin/go ]; then
    echo "▶ [1/5] Устанавливаем Go..."
    cd /tmp
    wget -q https://go.dev/dl/go1.22.5.linux-amd64.tar.gz
    tar -xzf go1.22.5.linux-amd64.tar.gz
    GOBIN=/tmp/go/bin/go
else
    GOBIN=$(command -v go 2>/dev/null || echo /tmp/go/bin/go)
fi
echo "✓ Go: $($GOBIN version)"

# ─── 2. Исходники ────────────────────────────────────────────────────────────
echo ""
echo "▶ [2/5] Клонируем репозиторий..."
if [ ! -d "$REMOTE_SRC/.git" ]; then
    git clone "$REPO_URL" "$REMOTE_SRC"
else
    cd "$REMOTE_SRC" && git fetch origin && git reset --hard origin/main
fi
cd "$REMOTE_SRC"
echo "✓ Исходники готовы"

# ─── 3. Сборка ───────────────────────────────────────────────────────────────
echo ""
echo "▶ [3/5] Сборка swarm-node..."
GOFLAGS=-mod=mod "$GOBIN" build -trimpath -ldflags="-s -w" \
    -o /usr/local/bin/swarm-node ./cmd/swarm-node/
chmod +x /usr/local/bin/swarm-node
echo "✓ $(du -sh /usr/local/bin/swarm-node | cut -f1) установлен в /usr/local/bin/swarm-node"

# ─── 4. Конфиг ───────────────────────────────────────────────────────────────
echo ""
echo "▶ [4/5] Настройка конфига..."
mkdir -p /etc/swarm /var/lib/swarm

if [ ! -f /etc/swarm/node-config.json ]; then
    cat > /etc/swarm/node-config.json << EOF
{
  "mode": "bootstrap",
  "listen_addr": ":${LISTEN_PORT}",
  "status_addr": ":${STATUS_PORT}",
  "identity_file": "/etc/swarm/identity.json",
  "max_peers": 200
}
EOF
    echo "✓ Конфиг создан: /etc/swarm/node-config.json"
else
    echo "✓ Конфиг уже существует (не перезаписываем)"
fi

# ─── 5. Systemd ──────────────────────────────────────────────────────────────
echo ""
echo "▶ [5/5] Systemd сервис..."

# TPROXY скрипты (нужны для service файла, на bootstrap просто ничего не делают)
mkdir -p /usr/local/share/swarm
cp install/tproxy-rules.sh install/tproxy-cleanup.sh /usr/local/share/swarm/
chmod +x /usr/local/share/swarm/tproxy-rules.sh /usr/local/share/swarm/tproxy-cleanup.sh

cp install/systemd/swarm-node.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable swarm-node
systemctl restart swarm-node
sleep 3

if systemctl is-active swarm-node &>/dev/null; then
    echo "✓ swarm-node запущен!"
else
    echo "❌ Ошибка запуска!"
    journalctl -u swarm-node -n 20 --no-pager
    exit 1
fi

# ─── Порты ───────────────────────────────────────────────────────────────────
echo ""
echo "▶ Открываем порты..."
if command -v ufw &>/dev/null; then
    ufw allow "${LISTEN_PORT}/udp" comment "swarm-node QUIC" 2>/dev/null || true
    ufw allow "${STATUS_PORT}/tcp" comment "swarm-node status" 2>/dev/null || true
    echo "✓ ufw: порты ${LISTEN_PORT}/udp и ${STATUS_PORT}/tcp открыты"
elif command -v iptables &>/dev/null; then
    iptables -I INPUT -p udp --dport "${LISTEN_PORT}" -j ACCEPT 2>/dev/null || true
    iptables -I INPUT -p tcp --dport "${STATUS_PORT}" -j ACCEPT 2>/dev/null || true
    echo "✓ iptables: порты открыты"
fi

# ─── NodeID ──────────────────────────────────────────────────────────────────
echo ""
NODE_ID=$(curl -s http://127.0.0.1:${STATUS_PORT}/api/status | \
    python3 -c "import sys,json; print(json.load(sys.stdin)['node_id'])" 2>/dev/null || echo "получить не удалось")

echo "╔══════════════════════════════════════════════════════════╗"
echo "║  ✅ Bootstrap узел готов!                               ║"
echo "╠══════════════════════════════════════════════════════════╣"
printf "║  IP:      %-46s ║\n" "$(curl -s ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}'):${LISTEN_PORT}"
printf "║  NodeID:  %-46s ║\n" "${NODE_ID:0:32}..."
echo "╠══════════════════════════════════════════════════════════╣"
echo "║  Добавь в конфиг клиента:                               ║"
echo "║  \"bootstrap_addrs\": [\"<ЭТО_IP>:${LISTEN_PORT}\"]               ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""
echo "Следующие шаги:"
echo "  1. Запиши NodeID выше — добавь как bootstrap_node_id в клиентский конфиг"
echo "  2. Проверка: curl http://$(hostname -I | awk '{print $1}'):${STATUS_PORT}/health"
echo "  3. Логи:    journalctl -u swarm-node -f"
