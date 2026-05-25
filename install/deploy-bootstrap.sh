#!/bin/bash
# deploy-bootstrap.sh — развёртывание S.W.A.R.M. bootstrap узла на VDS
#
# Использование:
#   scp src/ user@YOUR_VDS_IP:~/swarm-node-src/ -r
#   ssh user@YOUR_VDS_IP 'bash ~/swarm-node-src/install/deploy-bootstrap.sh'
#
set -euo pipefail

echo "=== S.W.A.R.M. Bootstrap Node Deploy ==="
echo "Сервер: $(hostname) $(hostname -I | awk '{print $1}')"

# ─── Зависимости ────────────────────────────────────────────────────────────
echo "[1] Установка зависимостей..."
apt-get update -q
apt-get install -y --no-install-recommends curl wget ufw

# ─── Go SDK ─────────────────────────────────────────────────────────────────
if ! command -v go &>/dev/null && [ ! -f /usr/local/go/bin/go ]; then
    echo "[2] Установка Go 1.22..."
    wget -q https://go.dev/dl/go1.22.4.linux-amd64.tar.gz -O /tmp/go.tar.gz
    tar -C /usr/local -xzf /tmp/go.tar.gz
    rm /tmp/go.tar.gz
    echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile.d/go.sh
fi
export PATH=$PATH:/usr/local/go/bin
echo "Go: $(go version)"

# ─── Конфиг директория ──────────────────────────────────────────────────────
echo "[3] Создание /etc/swarm/..."
mkdir -p /etc/swarm

# ─── Конфиг bootstrap узла ──────────────────────────────────────────────────
if [ ! -f /etc/swarm/node-config.json ]; then
    cat > /etc/swarm/node-config.json << 'EOF'
{
  "listen_addr": ":7437",
  "socks5_addr": "",
  "identity_file": "/etc/swarm/identity.json",
  "bootstrap_addr": "",
  "bootstrap_node_id": "",
  "max_peers": 200,
  "mode": "bootstrap"
}
EOF
    echo "[3] Конфиг создан: /etc/swarm/node-config.json"
else
    echo "[3] Конфиг уже существует, пропускаем"
fi

# ─── Открыть порт ───────────────────────────────────────────────────────────
echo "[4] Открываем порт 7437/udp (QUIC)..."
ufw allow 7437/udp comment 'SWARM QUIC' || true
ufw allow 7437/tcp comment 'SWARM TCP fallback' || true

# ─── Компиляция ─────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC_DIR="$(dirname "$SCRIPT_DIR")"

echo "[5] Компиляция swarm-node..."
cd "$SRC_DIR"
go build -o /usr/local/bin/swarm-node ./cmd/swarm-node/
echo "[5] Скомпилировано: /usr/local/bin/swarm-node ($(du -sh /usr/local/bin/swarm-node | cut -f1))"

# ─── Systemd ─────────────────────────────────────────────────────────────────
echo "[6] Установка systemd сервиса..."
cp "$SCRIPT_DIR/systemd/swarm-node.service" /etc/systemd/system/
systemctl daemon-reload
systemctl enable swarm-node
systemctl restart swarm-node

sleep 2
systemctl is-active swarm-node && echo "[6] ✅ swarm-node запущен!" || echo "[6] ❌ swarm-node не запустился. Проверьте: journalctl -u swarm-node -n 30"

# ─── NodeID ──────────────────────────────────────────────────────────────────
echo ""
echo "=== Готово! ==="
echo "Статус: systemctl status swarm-node"
echo "Логи:   journalctl -u swarm-node -f"
echo ""
echo "⚠️  После первого запуска NodeID сохранится в /etc/swarm/identity.json"
echo "   Запомните его — клиенты используют NodeID для верификации bootstrap сервера"
echo ""
echo "Чтобы получить NodeID запущенного узла:"
echo "   journalctl -u swarm-node | grep 'Новая идентичность\|Запуск узла'"
