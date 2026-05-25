#!/bin/bash
# switch-to-swarm.sh — переключает xray outbound на swarm-node SOCKS5
#
# БЫЛО:  home devices → xray tproxy → VLESS/Reality → VDS → интернет
# СТАЛО: home devices → xray tproxy → swarm-node:1090 → QUIC рой → интернет
#
# Безопасно: можно откатить через switch-to-vless.sh
# Требует: python3, работающий xray, запущенный swarm-node

set -euo pipefail

XRAY_CONFIG="/home/roman/xray-config.json"
BACKUP="${XRAY_CONFIG}.bak-$(date +%Y%m%d-%H%M%S)"
SWARM_SOCKS5="127.0.0.1"
SWARM_PORT=1090

# ─── Проверки ───────────────────────────────────────────────────────────────
echo "[1] Проверяем swarm-node на :$SWARM_PORT..."
if ! ss -tlnp | grep -q ":$SWARM_PORT"; then
    echo "❌ swarm-node не запущен на :$SWARM_PORT"
    echo "   Запусти: ~/swarm-node -config ~/swarm-config/node-config.json &"
    exit 1
fi
echo "    ✅ swarm-node слушает"

# ─── Бэкап ──────────────────────────────────────────────────────────────────
echo "[2] Бэкап конфига → $BACKUP"
cp "$XRAY_CONFIG" "$BACKUP"

# ─── Переключение outbound ───────────────────────────────────────────────────
echo "[3] Меняем swarm-out → socks5://127.0.0.1:$SWARM_PORT..."
python3 << PYEOF
import json, sys

with open('$XRAY_CONFIG') as f:
    cfg = json.load(f)

new_outbound = {
    "tag": "swarm-out",
    "protocol": "socks",
    "settings": {
        "servers": [{
            "address": "$SWARM_SOCKS5",
            "port": $SWARM_PORT
        }]
    }
}

for i, ob in enumerate(cfg['outbounds']):
    if ob.get('tag') == 'swarm-out':
        cfg['outbounds'][i] = new_outbound
        print(f"    Заменён outbound #{i}: vless → socks5")
        break
else:
    cfg['outbounds'].insert(0, new_outbound)
    print("    Добавлен новый outbound swarm-out")

with open('$XRAY_CONFIG', 'w') as f:
    json.dump(cfg, f, indent=2, ensure_ascii=False)
print("    Конфиг обновлён")
PYEOF

# ─── Перезапуск xray ─────────────────────────────────────────────────────────
echo "[4] Перезапускаем xray..."
sudo -n systemctl restart xray 2>/dev/null || {
    # Если нет passwordless sudo, перезапускаем xray напрямую
    pkill -HUP xray 2>/dev/null || true
    echo "    (SIGHUP отправлен xray)"
}
sleep 2

echo ""
echo "═══════════════════════════════════════"
echo "✅ Переключено на swarm-node!"
echo "═══════════════════════════════════════"
echo ""
echo "Весь трафик теперь идёт:"
echo "  домашние устройства"
echo "  → xray tproxy (12345)"
echo "  → swarm-node SOCKS5 (:$SWARM_PORT)"
echo "  → QUIC рой"
echo "  → интернет"
echo ""
echo "Проверка: curl --socks5 $SWARM_SOCKS5:$SWARM_PORT https://api.ipinfo.io"
echo "Откат:    bash ~/switch-to-vless.sh"
echo "Бэкап:    $BACKUP"
