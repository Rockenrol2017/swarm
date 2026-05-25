#!/bin/bash
# swarm-tproxy.sh — настройка TPROXY для swarm-node
#
# Режим: swarm-node принимает весь трафик от домашних устройств напрямую.
# Не требует xray! swarm-node = и прокси, и шифрование, и рой.
#
# Запускать с root. Используется как ExecStartPre в systemd.
#
set -euo pipefail

SWARM_TPROXY_PORT=12346      # порт куда перенаправляем (swarm-node TProxyAddr)
SWARM_MARK=0x233             # маркер для пакетов
LAN_IFACE="${LAN_IFACE:-eth0}"  # интерфейс к домашней сети (eth0, enp3s0, ...)
HOME_NET="${HOME_NET:-192.168.1.0/24}"  # твоя домашняя подсеть

# Не трогаем прямые соединения самой коробки к VDS
BOOTSTRAP_IP="${BOOTSTRAP_IP:-YOUR_VDS_IP}"  # IP твоего VDS
BOOTSTRAP_PORT="${BOOTSTRAP_PORT:-7437}"

# ─── Mangle table (TPROXY перенаправление) ────────────────────────────────

# Очищаем только наши правила (не трогаем xray)
iptables -t mangle -N SWARM_TPROXY 2>/dev/null || iptables -t mangle -F SWARM_TPROXY

# Пропускаем уже помеченные пакеты (чтобы не зациклиться)
iptables -t mangle -A SWARM_TPROXY -m mark --mark $SWARM_MARK -j RETURN

# Пропускаем локальный трафик
iptables -t mangle -A SWARM_TPROXY -d 127.0.0.0/8 -j RETURN
iptables -t mangle -A SWARM_TPROXY -d 192.168.0.0/16 -j RETURN
iptables -t mangle -A SWARM_TPROXY -d 10.0.0.0/8 -j RETURN
iptables -t mangle -A SWARM_TPROXY -d 172.16.0.0/12 -j RETURN

# Пропускаем трафик к bootstrap серверу (чтобы swarm мог подключиться к рою)
iptables -t mangle -A SWARM_TPROXY -d "$BOOTSTRAP_IP" -p udp --dport "$BOOTSTRAP_PORT" -j RETURN
iptables -t mangle -A SWARM_TPROXY -d "$BOOTSTRAP_IP" -p tcp --dport "$BOOTSTRAP_PORT" -j RETURN

# Пересылаем TCP трафик от LAN в TPROXY swarm-node
iptables -t mangle -A SWARM_TPROXY -p tcp -j TPROXY \
    --tproxy-mark $SWARM_MARK \
    --on-port "$SWARM_TPROXY_PORT" \
    --on-ip 127.0.0.1

# Применяем к форвардируемому (LAN) трафику
iptables -t mangle -A PREROUTING -i "$LAN_IFACE" -j SWARM_TPROXY

# ─── IP routing (чтобы помеченные пакеты шли к localhost) ─────────────────
ip rule add fwmark $SWARM_MARK lookup 100 2>/dev/null || true
ip route add local 0.0.0.0/0 dev lo table 100 2>/dev/null || true

# ─── MASQUERADE (NAT для исходящего трафика) ─────────────────────────────
iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE 2>/dev/null || true

# ─── IP forward ──────────────────────────────────────────────────────────
echo 1 > /proc/sys/net/ipv4/ip_forward

echo "[swarm-tproxy] TPROXY настроен: LAN → :$SWARM_TPROXY_PORT (swarm-node)"
echo "[swarm-tproxy] Интерфейс: $LAN_IFACE | Подсеть: $HOME_NET"
