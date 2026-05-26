#!/bin/bash
# tproxy-cleanup.sh — удаление iptables правил swarm-node при остановке сервиса.
#
# Вызывается из systemd ExecStopPost (с флагом '-' — ошибки игнорируются).

# Пропускаем если режим не client
CONFIG="/etc/swarm/node-config.json"
if [ -f "$CONFIG" ]; then
    MODE=$(python3 -c "import json; print(json.load(open('$CONFIG')).get('mode',''))" 2>/dev/null || true)
    if [ "$MODE" = "bootstrap" ] || [ "$MODE" = "relay" ]; then
        exit 0
    fi
fi

MARK=0x2
TABLE=101

iptables -t mangle -D PREROUTING -j SWARM_TPROXY 2>/dev/null || true
iptables -t mangle -F SWARM_TPROXY                2>/dev/null || true
iptables -t mangle -X SWARM_TPROXY                2>/dev/null || true
ip rule del fwmark "$MARK" lookup "$TABLE"         2>/dev/null || true
ip route del local default dev lo table "$TABLE"   2>/dev/null || true

echo "[swarm-tproxy] правила удалены"
