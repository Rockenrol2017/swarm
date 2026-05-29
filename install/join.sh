#!/bin/bash
# S.W.A.R.M. — one-command join script
# Usage: curl -fsSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install/join.sh | bash
# Or:    bash join.sh [--relay] [--tproxy]
#
# --relay   : также принимать чужой трафик (отдаёт 20% канала рою)
# --tproxy  : прозрачный прокси — весь трафик через рой (нужен root)

set -e

MODE="client"
RELAY_PCT=0
TPROXY=0

for arg in "$@"; do
  case $arg in
    --relay)  RELAY_PCT=20 ;;
    --tproxy) TPROXY=1 ;;
  esac
done

ARCH=$(uname -m)
case $ARCH in
  x86_64)  ARCH_TAG="linux-amd64" ;;
  aarch64) ARCH_TAG="linux-arm64" ;;
  armv7l)  ARCH_TAG="linux-arm" ;;
  *)       echo "Неподдерживаемая архитектура: $ARCH"; exit 1 ;;
esac

INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/swarm"
DATA_DIR="/var/lib/swarm"
BINARY="$INSTALL_DIR/swarm-node"
CONFIG="$CONFIG_DIR/node-config.json"
SERVICE="/etc/systemd/system/swarm-node.service"

echo "==> S.W.A.R.M. установка (режим: $MODE, arch: $ARCH_TAG)"

# 1. Скачать бинарник
echo "==> Скачиваем swarm-node..."
curl -fsSL -o /tmp/swarm-node \
  "https://github.com/Rockenrol2017/swarm/releases/latest/download/swarm-node-$ARCH_TAG"
chmod +x /tmp/swarm-node

# Проверка
/tmp/swarm-node -version 2>/dev/null || true

# Установить
install -m 755 /tmp/swarm-node "$BINARY"
rm /tmp/swarm-node

# cap_net_admin для TPROXY (если нужен)
if [ $TPROXY -eq 1 ]; then
  setcap cap_net_admin=+ep "$BINARY" 2>/dev/null || \
    echo "Предупреждение: setcap не удался — запусти вручную: sudo setcap cap_net_admin=+ep $BINARY"
fi

# 2. Создать директории
mkdir -p "$CONFIG_DIR" "$DATA_DIR"

# 3. Конфиг
TPROXY_LINE=""
if [ $TPROXY -eq 1 ]; then
  TPROXY_LINE='  "tproxy_addr": ":12346",'
fi

cat > "$CONFIG" << EOF
{
  "mode": "$MODE",
  "bootstrap_addrs": [
    "193.68.89.168:7437",
    "78.17.74.239:7437",
    "166.1.89.52:7437"
  ],
  "socks5_addr": ":1090",
$TPROXY_LINE
  "identity_file": "$CONFIG_DIR/identity.json",
  "status_addr": ":19090",
  "max_relay_percent": $RELAY_PCT
}
EOF

echo "==> Конфиг создан: $CONFIG"

# 4. systemd unit
cat > "$SERVICE" << 'UNIT'
[Unit]
Description=S.W.A.R.M. Node
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/swarm-node -config /etc/swarm/node-config.json
Restart=always
RestartSec=5
LimitNOFILE=65536
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT

# 5. Запустить
systemctl daemon-reload
systemctl enable swarm-node
systemctl restart swarm-node

echo ""
echo "✅ S.W.A.R.M. запущен!"
echo ""
echo "   SOCKS5 прокси: 127.0.0.1:1090"
echo "   Статус:        http://localhost:19090/api/status"
echo ""
echo "   Проверить подключение:"
echo "   curl --proxy socks5://127.0.0.1:1090 https://api.ipinfo.io/ip"
echo ""
echo "   Для браузера: Настройки → Прокси → SOCKS5 → 127.0.0.1:1090"

# Ждём подключения и показываем статус
sleep 5
if curl -sf http://127.0.0.1:19090/api/status > /dev/null 2>&1; then
  PEERS=$(curl -s http://127.0.0.1:19090/api/status | python3 -c "import sys,json; print(json.load(sys.stdin).get('peers',0))" 2>/dev/null || echo "?")
  echo "   Пиров в рое:   $PEERS"
fi
