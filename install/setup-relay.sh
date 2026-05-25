#!/bin/bash
# setup-relay.sh — настройка relay узла S.W.A.R.M.
#
# Relay отличается от bootstrap:
#   - Слушает входящие QUIC соединения (как bootstrap)
#   - И сам подключается к bootstrap (как client)
#   - Становится промежуточным узлом роя
#   - Client → Relay → Bootstrap → Internet (2 прыжка)
#
# Использование:
#   bash setup-relay.sh [listen_port] [bootstrap_addr]
#
# Пример:
#   bash setup-relay.sh 7438 YOUR_BOOTSTRAP_IP:7437

set -euo pipefail

LISTEN_PORT="${1:-7438}"
BOOTSTRAP="${2:-YOUR_BOOTSTRAP_IP:7437}"
CONFIG_DIR="/etc/swarm"
BINARY="/usr/local/bin/swarm-node"

echo "╔══════════════════════════════════════════╗"
echo "║   S.W.A.R.M. Relay Node Setup           ║"
echo "╚══════════════════════════════════════════╝"
echo "Listen: :$LISTEN_PORT"
echo "Bootstrap: $BOOTSTRAP"
echo ""

# Проверяем бинарник
if [ ! -f "$BINARY" ]; then
    echo "❌ $BINARY не найден. Запустите сначала deploy-bootstrap.sh"
    exit 1
fi

# Создаём конфиг
mkdir -p "$CONFIG_DIR"
cat > "$CONFIG_DIR/node-config.json" << CONF
{
  "listen_addr": ":$LISTEN_PORT",
  "socks5_addr": "",
  "tproxy_addr": "",
  "status_addr": ":19090",
  "identity_file": "$CONFIG_DIR/identity.json",
  "bootstrap_addr": "$BOOTSTRAP",
  "bootstrap_node_id": "",
  "max_peers": 100,
  "mode": "relay"
}
CONF
echo "✓ Конфиг: $CONFIG_DIR/node-config.json"

# Firewall
if command -v ufw &>/dev/null; then
    ufw allow ${LISTEN_PORT}/udp comment 'SWARM relay QUIC' 2>/dev/null || true
fi
iptables -I INPUT -p udp --dport $LISTEN_PORT -j ACCEPT 2>/dev/null || true
iptables -I INPUT -p tcp --dport $LISTEN_PORT -j ACCEPT 2>/dev/null || true
echo "✓ Порт $LISTEN_PORT открыт"

# Systemd unit
cat > /etc/systemd/system/swarm-node.service << SERVICE
[Unit]
Description=S.W.A.R.M. Relay Node
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
ExecStart=$BINARY -config $CONFIG_DIR/node-config.json
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=swarm-node

[Install]
WantedBy=multi-user.target
SERVICE

systemctl daemon-reload
systemctl enable swarm-node
systemctl restart swarm-node
sleep 2

if systemctl is-active swarm-node &>/dev/null; then
    echo "✅ Relay узел запущен!"
    echo ""
    journalctl -u swarm-node -n 8 --no-pager
    echo ""
    echo "NodeID (через ~5с):"
    sleep 3
    journalctl -u swarm-node | grep 'Запуск узла' | tail -1 | grep -o '[a-f0-9]\{16\}'
else
    echo "❌ Ошибка запуска"
    journalctl -u swarm-node -n 20 --no-pager
    exit 1
fi

echo ""
echo "Добавьте этот relay в конфиг клиентов как дополнительный bootstrap:"
echo "  \"bootstrap_addr\": \"$(curl -s ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}'):$LISTEN_PORT\""
