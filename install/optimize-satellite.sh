#!/bin/bash
# optimize-satellite.sh — оптимизация для спутникового канала SkyEdge
#
# Что делает:
#   1. Включает BBR congestion control (умная регулировка для высокого RTT)
#   2. Увеличивает TCP буферы (важно при RTT ~700мс: больше данных в полёте)
#   3. Устанавливает dnsmasq как DNS кэш (снижает количество DNS запросов)
#   4. Настраивает разумные timeouts для спутникового канала
#
# Применение:
#   sudo bash optimize-satellite.sh
#
# Параметры SkyEdge:
#   Лимит: 310 ГБ/мес полной скорости (после — плавное снижение)
#   Скорость: 20-40 Мбит/с
#   RTT: ~600-800мс
#   Джиттер: высокий — BBR с этим хорошо справляется

set -euo pipefail

[[ $EUID -ne 0 ]] && { echo "❌ Нужен root: sudo bash $0"; exit 1; }

echo "╔═══════════════════════════════════════════╗"
echo "║  S.W.A.R.M. — Оптимизация для спутника   ║"
echo "╚═══════════════════════════════════════════╝"
echo ""

# ─── 1. BBR Congestion Control ────────────────────────────────────────────
echo "▶ [1/4] BBR congestion control..."

KERNEL=$(uname -r | cut -d. -f1-2)
echo "  Ядро: $KERNEL"

# BBR требует ядро 4.9+
if modprobe tcp_bbr 2>/dev/null; then
    echo "  ✓ Модуль tcp_bbr загружен"
else
    echo "  ⚠ BBR модуль недоступен, пробуем cubic..."
fi

# Записываем настройки
cat > /etc/sysctl.d/99-swarm-satellite.conf << 'SYSCTL'
# ═══════════════════════════════════════════════════════════════
# S.W.A.R.M. — Оптимизация TCP для спутникового канала SkyEdge
# RTT ~600-800мс, лимит 310 ГБ/мес, скорость 20-40 Мбит/с
# ═══════════════════════════════════════════════════════════════

# ─── BBR Congestion Control ───────────────────────────────────
# BBR лучше cubic при высоком RTT и потерях (спутник).
# В отличие от cubic/reno, BBR не требует трёх дублирующих ACK.
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr

# ─── TCP буферы ───────────────────────────────────────────────
# Формула: буфер = bandwidth * RTT
# 40 Мбит/с * 0.8с = 4 МБ → увеличиваем до 16 МБ для запаса
#
# receive buffer max = 16 МБ
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
# TCP auto-tuning до 16 МБ
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216
# Общий буфер ядра
net.core.netdev_max_backlog = 5000
net.core.optmem_max = 40960

# ─── TCP Tuning для высокого RTT ──────────────────────────────
# Window scaling — обязательно при RTT > 300мс
net.ipv4.tcp_window_scaling = 1
# SACK — Fast retransmit при потерях (без ожидания timeout)
net.ipv4.tcp_sack = 1
# Timestamps — для точного RTT измерения
net.ipv4.tcp_timestamps = 1
# ECN — явная нотификация о перегрузке (если роутер поддерживает)
net.ipv4.tcp_ecn = 1
# Быстрый старт TCP — меньше ждём при первом подключении
net.ipv4.tcp_fastopen = 3
# Число повторных попыток SYN при установке соединения
# Спутник может дропнуть первый SYN — даём 4 попытки
net.ipv4.tcp_syn_retries = 4
net.ipv4.tcp_synack_retries = 3

# ─── Таймауты ─────────────────────────────────────────────────
# Закрытие зависших соединений (спутник часто дропает)
net.ipv4.tcp_keepalive_time = 120
net.ipv4.tcp_keepalive_intvl = 30
net.ipv4.tcp_keepalive_probes = 5
# FIN_WAIT таймаут
net.ipv4.tcp_fin_timeout = 30

# ─── Forwarding (для домашнего сервера-шлюза) ─────────────────
net.ipv4.ip_forward = 1
SYSCTL

# Применяем немедленно
sysctl -p /etc/sysctl.d/99-swarm-satellite.conf 2>&1 | grep -v "^$" | head -20

# Проверяем что BBR реально включился
CURRENT_CC=$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo "unknown")
echo ""
echo "  TCP congestion control: $CURRENT_CC"
if [[ "$CURRENT_CC" == "bbr" ]]; then
    echo "  ✅ BBR активен"
else
    echo "  ⚠ BBR не включился (cubic работает тоже нормально)"
fi

# ─── 2. dnsmasq DNS кэш ───────────────────────────────────────────────────
echo ""
echo "▶ [2/4] dnsmasq DNS кэш..."

if command -v dnsmasq &>/dev/null; then
    echo "  ✓ dnsmasq уже установлен"
else
    echo "  Устанавливаем dnsmasq..."
    apt-get install -y -q dnsmasq
    echo "  ✓ dnsmasq установлен"
fi

# Настройка dnsmasq
cat > /etc/dnsmasq.d/swarm-satellite.conf << 'DNSMASQ'
# S.W.A.R.M. DNS кэш для спутника
# Цель: не тратить трафик и RTT на повторные DNS запросы
# (спутниковый DNS запрос ≈ 1-3мс*2 = 600мс+ RTT добавляет)

# Кэш 1000 записей (на обычном сервере хватает на часы работы)
cache-size=1000

# Минимальный TTL 300с — кешируем даже если домен говорит TTL=0
min-cache-ttl=300

# Upstream DNS (DoH через xray или прямой Cloudflare)
# Если xray работает, он сам делает DoH. Если нет — Cloudflare.
no-resolv
server=1.1.1.1
server=8.8.8.8

# Логировать кэш хиты для отладки (можно выключить)
# log-queries

# Слушаем только локально (не делаем публичный DNS resolver)
listen-address=127.0.0.1
bind-interfaces
DNSMASQ

# Проверяем что не конфликтует с systemd-resolved
if systemctl is-active systemd-resolved &>/dev/null; then
    echo "  Обнаружен systemd-resolved, настраиваем совместимость..."
    # Отключаем DNS stub resolver systemd (он занимает порт 53)
    mkdir -p /etc/systemd/resolved.conf.d/
    cat > /etc/systemd/resolved.conf.d/no-stub.conf << 'RESOLVED'
[Resolve]
DNSStubListener=no
RESOLVED
    systemctl restart systemd-resolved
    echo "  ✓ systemd-resolved stub отключён"
fi

# Включаем и запускаем dnsmasq
systemctl enable dnsmasq
systemctl restart dnsmasq
sleep 1

if systemctl is-active dnsmasq &>/dev/null; then
    echo "  ✅ dnsmasq запущен и кэширует DNS"
    # Проверяем что порт 53 слушается
    ss -tulnp | grep ':53' | grep dnsmasq && echo "  ✓ Порт 53 активен" || true
else
    echo "  ❌ dnsmasq не запустился"
    journalctl -u dnsmasq -n 10 --no-pager
fi

# ─── 3. Проверка результата ───────────────────────────────────────────────
echo ""
echo "▶ [3/4] Проверка MTU и PMTUD..."

# Спутниковые каналы иногда требуют меньший MTU
IFACE=$(ip route | grep default | awk '{print $5}' | head -1)
if [[ -n "$IFACE" ]]; then
    CURRENT_MTU=$(cat /sys/class/net/"$IFACE"/mtu 2>/dev/null || echo "?")
    echo "  Интерфейс: $IFACE, MTU: $CURRENT_MTU"
    # Для спутника MTU 1452-1472 обычно оптимален
    # Не меняем автоматически — пользователь должен решить сам
    if [[ "$CURRENT_MTU" -gt 1472 ]] 2>/dev/null; then
        echo "  ℹ MTU $CURRENT_MTU может быть велик для спутника"
        echo "    Попробуй: sudo ip link set $IFACE mtu 1452"
        echo "    (проверь скорость до и после)"
    fi
fi

# ─── 4. Оптимизация QUIC буферов ─────────────────────────────────────────
echo ""
echo "▶ [4/4] UDP буферы для QUIC..."

# quic-go рекомендует увеличить UDP receive buffer для высокопропускных каналов
# Для спутника важнее иметь большой буфер чтобы не терять пакеты при джиттере
cat >> /etc/sysctl.d/99-swarm-satellite.conf << 'QUICSYSCTL'

# ─── UDP буферы для QUIC ──────────────────────────────────────
# quic-go: рекомендуется минимум 7.6 МБ для оптимальной работы
net.core.rmem_default = 7864320
net.core.wmem_default = 7864320
QUICSYSCTL

sysctl net.core.rmem_default 2>/dev/null | tail -1
echo "  ✓ UDP буферы увеличены"

sysctl -p /etc/sysctl.d/99-swarm-satellite.conf &>/dev/null

# ─── Финальный отчёт ─────────────────────────────────────────────────────
echo ""
echo "╔═══════════════════════════════════════════╗"
echo "║        ОПТИМИЗАЦИЯ ЗАВЕРШЕНА              ║"
echo "╠═══════════════════════════════════════════╣"
printf "║ BBR:     %-32s║\n" "$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)"
printf "║ dnsmasq: %-32s║\n" "$(systemctl is-active dnsmasq 2>/dev/null)"
printf "║ rmem_max: %-31s║\n" "$(sysctl -n net.core.rmem_max 2>/dev/null)"
echo "╠═══════════════════════════════════════════╣"
echo "║ СЛЕДУЮЩИЕ ШАГИ:                           ║"
echo "║   sudo systemctl restart swarm-node       ║"
echo "║   curl -s http://127.0.0.1:19090/api/status ║"
echo "║   # Проверить latency_ms — должно быть    ║"
echo "║   # ~600-800мс для нормального спутника   ║"
echo "╚═══════════════════════════════════════════╝"
