#!/bin/bash
# tproxy-rules.sh — настройка iptables правил для swarm-node TPROXY (:12346)
#
# Вызывается из systemd ExecStartPre (с флагом '-' — ошибки не прерывают старт).
# Безопасно запускать повторно (идемпотентный).
#
# Использует mark 0x2 и таблицу 101, чтобы не конфликтовать с xray (mark 0x1, таблица 100).

set -e

TPROXY_PORT=12346
MARK=0x2
TABLE=101

# ─── Маршрутизация TPROXY-маркированных пакетов через loopback ───────────
ip rule del fwmark "$MARK" lookup "$TABLE" 2>/dev/null || true
ip rule add fwmark "$MARK" lookup "$TABLE"
ip route del local default dev lo table "$TABLE" 2>/dev/null || true
ip route add local default dev lo table "$TABLE"

# ─── Цепочка iptables ────────────────────────────────────────────────────
iptables -t mangle -N SWARM_TPROXY 2>/dev/null || true
iptables -t mangle -F SWARM_TPROXY

# Пропускаем локальный и частный трафик без перехвата
iptables -t mangle -A SWARM_TPROXY -d 127.0.0.0/8     -j RETURN
iptables -t mangle -A SWARM_TPROXY -d 192.168.0.0/16  -j RETURN
iptables -t mangle -A SWARM_TPROXY -d 10.0.0.0/8      -j RETURN
iptables -t mangle -A SWARM_TPROXY -d 172.16.0.0/12   -j RETURN

# UDP DNS — не перехватываем (SOCKS5 UDP ASSOCIATE не поддерживается)
iptables -t mangle -A SWARM_TPROXY -p udp --dport 53  -j RETURN

# TCP → TPROXY на порт swarm-node
iptables -t mangle -A SWARM_TPROXY -p tcp \
    -j TPROXY --tproxy-mark "$MARK/$MARK" --on-port "$TPROXY_PORT"

# ─── Подключаем цепочку к PREROUTING (без дублей) ────────────────────────
iptables -t mangle -D PREROUTING -j SWARM_TPROXY 2>/dev/null || true
iptables -t mangle -A PREROUTING -j SWARM_TPROXY

echo "[swarm-tproxy] правила установлены (port :$TPROXY_PORT, mark $MARK)"
