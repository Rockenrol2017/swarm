#!/bin/bash
# tproxy-cleanup.sh — удаление iptables правил swarm-node при остановке сервиса.
#
# Вызывается из systemd ExecStopPost (с флагом '-' — ошибки игнорируются).

MARK=0x2
TABLE=101

iptables -t mangle -D PREROUTING -j SWARM_TPROXY 2>/dev/null || true
iptables -t mangle -F SWARM_TPROXY                2>/dev/null || true
iptables -t mangle -X SWARM_TPROXY                2>/dev/null || true
ip rule del fwmark "$MARK" lookup "$TABLE"         2>/dev/null || true
ip route del local default dev lo table "$TABLE"   2>/dev/null || true

echo "[swarm-tproxy] правила удалены"
