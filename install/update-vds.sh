#!/bin/bash
# update-vds.sh — обновление VDS bootstrap из GitHub
#
# Запускать прямо на VDS:
#   bash ~/swarm-node-src/install/update-vds.sh
#
# При первом запуске клонирует репозиторий, при следующих — git pull + rebuild.

set -euo pipefail

REMOTE_SRC=~/swarm-node-src
REPO_URL="https://github.com/Rockenrol2017/swarm"

# ─── Найти Go ────────────────────────────────────────────────────────────────
find_go() {
    for candidate in /tmp/go/bin/go /usr/local/go/bin/go ~/go/bin/go; do
        if [ -x "$candidate" ]; then
            echo "$candidate"
            return
        fi
    done
    if command -v go &>/dev/null; then
        command -v go
        return
    fi
    echo ""
}

GOBIN=$(find_go)

if [ -z "$GOBIN" ]; then
    echo "→ Go не найден, устанавливаем..."
    cd /tmp
    wget -q https://go.dev/dl/go1.22.5.linux-amd64.tar.gz
    tar -xzf go1.22.5.linux-amd64.tar.gz
    GOBIN=/tmp/go/bin/go
fi

echo "→ Go: $($GOBIN version)"

# ─── Получить исходники ───────────────────────────────────────────────────────
if [ ! -d "$REMOTE_SRC/.git" ]; then
    echo "→ Первый запуск: клонируем $REPO_URL..."
    git clone "$REPO_URL" "$REMOTE_SRC"
else
    echo "→ Обновляем исходники..."
    cd "$REMOTE_SRC" && git pull
fi

cd "$REMOTE_SRC"

# ─── Сборка ───────────────────────────────────────────────────────────────────
echo "→ Сборка swarm-node..."
GOFLAGS=-mod=mod "$GOBIN" build -trimpath -ldflags="-s -w" \
    -o /tmp/swarm-node-new ./cmd/swarm-node/
echo "   $(du -sh /tmp/swarm-node-new | cut -f1) — готово"

# ─── Деплой ───────────────────────────────────────────────────────────────────
echo "→ Останавливаем сервис..."
systemctl stop swarm-node

echo "→ Устанавливаем бинарник..."
mv /tmp/swarm-node-new /usr/local/bin/swarm-node
chmod +x /usr/local/bin/swarm-node

echo "→ Обновляем systemd unit..."
cp install/systemd/swarm-node.service /etc/systemd/system/
systemctl daemon-reload

echo "→ Запускаем..."
systemctl start swarm-node
sleep 3

if systemctl is-active swarm-node &>/dev/null; then
    echo "✅ VDS bootstrap обновлён!"
    journalctl -u swarm-node -n 5 --no-pager
    echo ""
    curl -s http://127.0.0.1:19090/api/status | python3 -m json.tool 2>/dev/null || \
        curl -s http://127.0.0.1:19090/health
else
    echo "❌ Сервис не запустился!"
    journalctl -u swarm-node -n 20 --no-pager
    exit 1
fi
