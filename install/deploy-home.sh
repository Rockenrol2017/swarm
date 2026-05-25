#!/bin/bash
# deploy-home.sh — собирает swarm-node и деплоит на домашний сервер
# Запускать из D:\S.W.A.R.M\src на Windows (через Git Bash / WSL)
# или из ~/swarm-node-src на домашнем сервере

set -e

HOME_HOST="${1:-user@YOUR_HOME_SERVER_IP}"
BINARY="swarm-node"
REMOTE_BIN="/usr/local/bin/swarm-node"
SERVICE_NAME="swarm-node"

echo "=== S.W.A.R.M. home deploy ==="
echo "Цель: $HOME_HOST"

# 1. Собираем бинарник (если запуск с Windows — нужно GOOS/GOARCH)
if [[ "$(uname -s)" == *"MINGW"* ]] || [[ "$(uname -s)" == *"MSYS"* ]]; then
    echo "→ Кросс-компиляция для Linux amd64..."
    GOOS=linux GOARCH=amd64 go build -o "$BINARY" ./cmd/swarm-node/
else
    echo "→ Компиляция на сервере (использую remote build)..."
fi

# 2. Копируем бинарник
echo "→ Отправляем бинарник..."
ssh "$HOME_HOST" "systemctl is-active $SERVICE_NAME 2>/dev/null && sudo systemctl stop $SERVICE_NAME; true"
scp "$BINARY" "$HOME_HOST:/tmp/swarm-node-new"
ssh "$HOME_HOST" "sudo mv /tmp/swarm-node-new $REMOTE_BIN && sudo chmod +x $REMOTE_BIN"

# 3. Устанавливаем systemd unit
echo "→ Устанавливаем systemd unit..."
scp "$(dirname "$0")/systemd/swarm-node.service" "$HOME_HOST:/tmp/swarm-node.service"
ssh "$HOME_HOST" "sudo cp /tmp/swarm-node.service /etc/systemd/system/ && sudo systemctl daemon-reload"

# 4. Создаём конфиг если не существует
echo "→ Проверяем конфиг..."
ssh "$HOME_HOST" "
if [ ! -f /etc/swarm/node-config.json ]; then
    sudo mkdir -p /etc/swarm
    sudo tee /etc/swarm/node-config.json > /dev/null << 'CONF'
{
  \"listen_addr\": \"\",
  \"socks5_addr\": \":1090\",
  \"tproxy_addr\": \"\",
  \"identity_file\": \"/etc/swarm/identity.json\",
  \"bootstrap_addr\": \"YOUR_VDS_IP:7437\",
  \"bootstrap_node_id\": \"\",
  \"max_peers\": 50,
  \"mode\": \"client\"
}
CONF
    echo '✓ Конфиг создан: /etc/swarm/node-config.json'
else
    echo '✓ Конфиг уже существует (не перезаписываем)'
fi
"

# 5. Включаем и запускаем
echo "→ Запускаем сервис..."
ssh "$HOME_HOST" "sudo systemctl enable --now $SERVICE_NAME"

echo ""
echo "=== Готово! ==="
echo "Статус: ssh $HOME_HOST sudo systemctl status $SERVICE_NAME"
echo "Логи:   ssh $HOME_HOST sudo journalctl -u $SERVICE_NAME -f"
