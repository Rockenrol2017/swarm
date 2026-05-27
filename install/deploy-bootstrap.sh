#!/bin/bash
# deploy-bootstrap.sh — быстрый деплой swarm-node на новый VDS из готового бинарника.
#
# Использование:
#   bash install/deploy-bootstrap.sh <VDS_IP> [имя_узла]
#
# Пример:
#   bash install/deploy-bootstrap.sh 78.17.74.239 germany
#   bash install/deploy-bootstrap.sh 166.1.89.52 usa
#
# Требования:
#   - Бинарник /usr/local/bin/swarm-node уже собран (go build на Linux)
#   - SSH доступ root@VDS_IP (ключ или пароль)
#
set -euo pipefail

# ─── Аргументы ──────────────────────────────────────────────────────────────
VDS_IP="${1:-}"
NODE_NAME="${2:-bootstrap}"

if [ -z "$VDS_IP" ]; then
    echo "Usage: bash install/deploy-bootstrap.sh <VDS_IP> [имя_узла]"
    echo "Example: bash install/deploy-bootstrap.sh 78.17.74.239 germany"
    exit 1
fi

echo "=== S.W.A.R.M. Bootstrap Deploy → $VDS_IP ($NODE_NAME) ==="

# ─── Проверяем бинарник ──────────────────────────────────────────────────────
LOCAL_BIN="/usr/local/bin/swarm-node"
if [ ! -f "$LOCAL_BIN" ]; then
    echo "❌ Бинарник не найден: $LOCAL_BIN"
    echo "   Сначала собери: go build -o /usr/local/bin/swarm-node ./cmd/swarm-node/"
    exit 1
fi
echo "[1] Бинарник: $LOCAL_BIN ($(du -sh "$LOCAL_BIN" | cut -f1))"

# ─── Копируем бинарник ──────────────────────────────────────────────────────
echo "[2] Копируем на $VDS_IP..."
scp "$LOCAL_BIN" "root@$VDS_IP:/usr/local/bin/swarm-node"
echo "[2] ✅ Бинарник скопирован"

# ─── Настройка сервера через SSH ─────────────────────────────────────────────
echo "[3] Настраиваем сервер..."
ssh "root@$VDS_IP" bash << 'ENDSSH'
set -euo pipefail

echo "  → chmod..."
chmod +x /usr/local/bin/swarm-node

echo "  → Создаём каталоги..."
mkdir -p /etc/swarm /var/lib/swarm

echo "  → Конфиг bootstrap..."
# Записываем только если ещё нет (не перезаписываем существующий)
if [ ! -f /etc/swarm/node-config.json ]; then
    cat > /etc/swarm/node-config.json << 'EOF'
{
  "listen_addr":     ":7437",
  "socks5_addr":     "",
  "tproxy_addr":     "",
  "identity_file":   "/etc/swarm/identity.json",
  "max_peers":       200,
  "mode":            "bootstrap",
  "status_addr":     ":19090",
  "max_relay_percent": 100
}
EOF
    echo "  → Конфиг создан"
else
    echo "  → Конфиг уже есть, пропускаем"
fi

echo "  → systemd unit..."
cat > /etc/systemd/system/swarm-node.service << 'EOF'
[Unit]
Description=S.W.A.R.M. Bootstrap Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/swarm-node -config /etc/swarm/node-config.json
Restart=always
RestartSec=5
LimitNOFILE=65536
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

echo "  → Firewall (ufw / iptables)..."
# ufw если есть, иначе iptables
if command -v ufw &>/dev/null; then
    ufw allow 7437/udp comment 'SWARM QUIC' 2>/dev/null || true
    ufw allow 19090/tcp comment 'SWARM Status' 2>/dev/null || true
    echo "  → ufw правила добавлены"
else
    iptables -I INPUT -p udp --dport 7437 -j ACCEPT 2>/dev/null || true
    iptables -I INPUT -p tcp --dport 19090 -j ACCEPT 2>/dev/null || true
    echo "  → iptables правила добавлены (не persistent)"
fi

echo "  → Включаем и запускаем сервис..."
systemctl daemon-reload
systemctl enable swarm-node
systemctl restart swarm-node
sleep 2

echo "  → Проверка статуса..."
systemctl is-active swarm-node
ENDSSH

# ─── Верификация ─────────────────────────────────────────────────────────────
echo "[4] Верификация..."

# Health check
if curl -sf --max-time 10 "http://$VDS_IP:19090/health" > /dev/null; then
    echo "[4] ✅ /health — OK"
else
    echo "[4] ⚠️  /health недоступен (возможно порт 19090 закрыт провайдером)"
fi

# Внешний IP
REMOTE_IP=$(ssh "root@$VDS_IP" 'curl -sf --max-time 5 ifconfig.me || echo unknown')
echo "[4] Внешний IP сервера: $REMOTE_IP"

# NodeID из логов
echo "[4] NodeID (первый запуск создаёт identity):"
ssh "root@$VDS_IP" 'journalctl -u swarm-node -n 20 --no-pager 2>/dev/null | grep -E "NodeID|identity|Запуск|запущен" | head -5 || echo "  (логи ещё не появились, подождите несколько секунд)"'

echo ""
echo "=== Готово! Узел $NODE_NAME ($VDS_IP) задеплоен ==="
echo ""
echo "Полезные команды на сервере (ssh root@$VDS_IP):"
echo "  systemctl status swarm-node"
echo "  journalctl -u swarm-node -f"
echo "  curl -s http://localhost:19090/api/status | python3 -m json.tool"
echo ""
echo "⚠️  Добавь IP в bootstrap_addrs клиента после верификации:"
echo "  \"bootstrap_addrs\": [\"...\", \"$VDS_IP:7437\"]"
