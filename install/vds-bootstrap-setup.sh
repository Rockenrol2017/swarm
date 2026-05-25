#!/bin/bash
# vds-bootstrap-setup.sh
# Запускать на VDS: bash <(curl -fsSL ...) или скопировать и запустить.
# Разворачивает S.W.A.R.M. bootstrap узел на Ubuntu 22.04.
set -euo pipefail

SWARM_PORT=7437
SWARM_DIR="/opt/swarm-node"
CONFIG_DIR="/etc/swarm"
BINARY="/usr/local/bin/swarm-node"
GO_SDK="$HOME/go_sdk"

echo "╔══════════════════════════════════════╗"
echo "║  S.W.A.R.M. Bootstrap Node Setup    ║"
echo "╚══════════════════════════════════════╝"
echo "Сервер: $(hostname -I | awk '{print $1}')"
echo ""

# ─── 1. Go SDK ───────────────────────────────────────────────────────────────
if command -v go &>/dev/null; then
    echo "[1] Go найден: $(go version)"
elif [ -f "$GO_SDK/bin/go" ]; then
    export PATH="$GO_SDK/bin:$PATH"
    echo "[1] Go найден в $GO_SDK: $($GO_SDK/bin/go version)"
else
    echo "[1] Устанавливаем Go 1.22.4..."
    wget -q https://go.dev/dl/go1.22.4.linux-amd64.tar.gz -O /tmp/go.tar.gz
    mkdir -p "$GO_SDK"
    tar -C "$GO_SDK" --strip-components=1 -xzf /tmp/go.tar.gz
    rm /tmp/go.tar.gz
    export PATH="$GO_SDK/bin:$PATH"
    echo 'export PATH="$HOME/go_sdk/bin:$PATH"' >> ~/.bashrc
    echo "[1] Go установлен: $(go version)"
fi
export PATH="${GO_SDK}/bin:$PATH"

# ─── 2. Директории и конфиг ──────────────────────────────────────────────────
echo "[2] Создаём директории..."
mkdir -p "$SWARM_DIR" "$CONFIG_DIR"

if [ ! -f "$CONFIG_DIR/node-config.json" ]; then
    cat > "$CONFIG_DIR/node-config.json" << 'CONF'
{
  "listen_addr": ":7437",
  "socks5_addr": "",
  "identity_file": "/etc/swarm/identity.json",
  "bootstrap_addr": "",
  "bootstrap_node_id": "",
  "max_peers": 200,
  "mode": "bootstrap"
}
CONF
    echo "[2] Конфиг создан: $CONFIG_DIR/node-config.json"
else
    echo "[2] Конфиг уже есть, пропускаем"
fi

# ─── 3. Получение исходного кода ─────────────────────────────────────────────
echo "[3] Копируем исходный код..."
# Если папка уже есть — обновляем, иначе создаём
mkdir -p "$SWARM_DIR"/{pkg/{swarmproto,swarmnode},cmd/swarm-node}

# Проверяем что файлы уже скопированы (через scp до запуска скрипта)
if [ ! -f "$SWARM_DIR/go.mod" ]; then
    echo ""
    echo "❌ Исходный код не найден в $SWARM_DIR!"
    echo "   Скопируйте исходники перед запуском скрипта:"
    echo "   scp -r swarm-node-src/* user@VDS:$SWARM_DIR/"
    exit 1
fi

# ─── 4. Сборка ───────────────────────────────────────────────────────────────
echo "[4] Скачиваем зависимости и собираем (может занять 2-5 минут)..."
cd "$SWARM_DIR"
GOPROXY=direct go mod tidy
GOPROXY=direct go build -o "$BINARY" ./cmd/swarm-node/
echo "[4] ✅ Скомпилировано: $BINARY ($(du -sh $BINARY | cut -f1))"

# ─── 5. Systemd ──────────────────────────────────────────────────────────────
echo "[5] Устанавливаем systemd сервис..."
cat > /etc/systemd/system/swarm-node.service << SERVICE
[Unit]
Description=S.W.A.R.M. Bootstrap Node
After=network-online.target
Wants=network-online.target

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

if systemctl is-active --quiet swarm-node; then
    echo "[5] ✅ swarm-node запущен!"
else
    echo "[5] ❌ Ошибка запуска. Логи:"
    journalctl -u swarm-node -n 20 --no-pager
    exit 1
fi

# ─── 6. Firewall ─────────────────────────────────────────────────────────────
echo "[6] Открываем порт $SWARM_PORT..."
if command -v ufw &>/dev/null; then
    ufw allow ${SWARM_PORT}/udp comment 'SWARM QUIC bootstrap' 2>/dev/null || true
    ufw allow ${SWARM_PORT}/tcp comment 'SWARM TCP fallback' 2>/dev/null || true
fi
# Прямые правила iptables (работают всегда)
iptables -I INPUT -p udp --dport $SWARM_PORT -j ACCEPT 2>/dev/null || true
iptables -I INPUT -p tcp --dport $SWARM_PORT -j ACCEPT 2>/dev/null || true

# ─── 7. NodeID ───────────────────────────────────────────────────────────────
echo ""
echo "═══════════════════════════════════════"
echo "✅ Bootstrap узел запущен!"
echo "═══════════════════════════════════════"
echo ""
echo "Адрес для клиентов: $(curl -s ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}'):$SWARM_PORT"
echo ""
echo "NodeID узла (через несколько секунд после старта):"
echo "  journalctl -u swarm-node | grep 'Запуск узла'"
echo ""
echo "Статус:  systemctl status swarm-node"
echo "Логи:    journalctl -u swarm-node -f"
echo ""
echo "⚠️  Запишите NodeID — он нужен клиентам для bootstrap_node_id в конфиге"
