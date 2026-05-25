#!/bin/bash
# switch-to-vless.sh — откат обратно на VLESS/Reality если что-то пошло не так
set -euo pipefail

XRAY_CONFIG="/home/roman/xray-config.json"
BACKUP_DIR="/home/roman"

# Ищем последний бэкап
LATEST_BACKUP=$(ls -t ${BACKUP_DIR}/xray-config.json.bak-* 2>/dev/null | head -1)

if [ -z "$LATEST_BACKUP" ]; then
    echo "❌ Бэкап не найден в $BACKUP_DIR"
    exit 1
fi

echo "Откатываем к: $LATEST_BACKUP"
cp "$LATEST_BACKUP" "$XRAY_CONFIG"
sudo -n systemctl restart xray 2>/dev/null || pkill -HUP xray
sleep 2
echo "✅ Откат выполнен. Трафик снова через VLESS/Reality → VDS"
