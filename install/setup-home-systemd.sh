#!/bin/bash
# setup-home-systemd.sh
# Запускать ЛОКАЛЬНО на домашнем сервере с паролем sudo
# Устанавливает swarm-node как systemd сервис

set -euo pipefail

echo "=== Установка swarm-node systemd сервис ==="

# Проверяем что бинарник собран
if [ ! -f ~/bin/swarm-node ]; then
    echo "❌ ~/bin/swarm-node не найден!"
    echo "   Сначала запустите: bash ~/swarm-node-src/install/setup-home-systemd.sh"
    exit 1
fi

# Переносим бинарник в /usr/local/bin/
echo "→ Устанавливаем бинарник в /usr/local/bin/..."
sudo cp ~/bin/swarm-node /usr/local/bin/swarm-node
sudo chmod +x /usr/local/bin/swarm-node
echo "  ✓ /usr/local/bin/swarm-node ($(du -sh /usr/local/bin/swarm-node | cut -f1))"

# Создаём /etc/swarm/ конфиг
echo "→ Создаём /etc/swarm/..."
sudo mkdir -p /etc/swarm

if [ ! -f /etc/swarm/node-config.json ]; then
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

# Переносим identity если была сохранена в ~/.config/swarm/
if [ -f ~/.config/swarm/identity.json ] && [ ! -f /etc/swarm/identity.json ]; then
    sudo cp ~/.config/swarm/identity.json /etc/swarm/identity.json
    echo "  ✓ Identity перенесён в /etc/swarm/"
fi

# Устанавливаем systemd unit
echo "→ Устанавливаем systemd unit..."
sudo cp ~/swarm-node-src/install/systemd/swarm-node.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable swarm-node
echo "  ✓ swarm-node включён в автозапуск"

# Останавливаем старый процесс
if pgrep -x swarm-node &>/dev/null; then
    pkill -x swarm-node || true
    sleep 1
    echo "  → Убит старый процесс swarm-node"
fi

# Запускаем через systemd
echo "→ Запускаем swarm-node..."
sudo systemctl start swarm-node
sleep 2

if systemctl is-active swarm-node &>/dev/null; then
    echo ""
    echo "✅ swarm-node запущен через systemd!"
    echo ""
    journalctl -u swarm-node -n 15 --no-pager
    echo ""
    echo "Команды:"
    echo "  Статус:  sudo systemctl status swarm-node"
    echo "  Логи:    sudo journalctl -u swarm-node -f"
    echo "  Стоп:    sudo systemctl stop swarm-node"
else
    echo "❌ swarm-node не запустился!"
    journalctl -u swarm-node -n 30 --no-pager
    exit 1
fi
