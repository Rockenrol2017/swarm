# S.W.A.R.M.
### Secure · Worldwide · Anonymous · Routing · Mesh

> Децентрализованная P2P сеть для приватности и безопасности.
> Чем больше участников — тем сильнее и надёжнее.

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.22+-blue.svg)](https://golang.org)
[![Status](https://img.shields.io/badge/Status-Alpha-orange.svg)]()

---

## Что это

S.W.A.R.M. — самохостируемая децентрализованная меш-сеть на Go.
Создаёт зашифрованный туннель между вашими устройствами и выходными узлами
через QUIC транспорт и шифрование ChaCha20-Poly1305.

Каждый участник усиляет сеть. Нет центральных серверов.
Нет единой точки отказа.

---

## Как работает

```
[Устройства — телефон, ноутбук, телевизор, консоль]
          ↓ прозрачный прокси (без настройки на устройствах)
[Узел S.W.A.R.M. — домашний сервер]
          ↓ зашифрованный QUIC туннель
          ↓ ChaCha20-Poly1305 + X25519 обмен ключами
[Bootstrap узел — VPS в другой стране]
          ↓
      [Интернет]
```

Все устройства в вашей сети защищены автоматически.
Ничего не нужно устанавливать на каждом устройстве.

---

## Возможности

- **Нулевая настройка устройств** — настройте один раз на сервере, все устройства защищены
- **QUIC транспорт** — быстрый, современный, UDP
- **ChaCha20-Poly1305** — аутентифицированное шифрование, быстрое на любом железе
- **X25519 + Ed25519** — современный обмен ключами и подпись идентичности
- **Прозрачный прокси** — перехватывает трафик на уровне ОС (TPROXY)
- **3 режима узла** — bootstrap, relay, client
- **2-hop relay** — Client → Relay → Bootstrap → Internet
- **Мониторинг трафика** — счётчики день/месяц, поддержка лимитных тарифов
- **RTT проба** — мониторинг качества туннеля каждые 30 секунд
- **Веб-дашборд** — реальная статистика на порту 8081
- **Оптимизация для спутника** — BBR, большие TCP буферы, DNS кэш
- **Открытый код** — GPL v3, проверьте всё сами

---

## Быстрый старт

### Требования

- Linux (Ubuntu 20.04+ / Debian 12+)
- Go 1.22+
- Root доступ (для TPROXY)

### Bootstrap узел (VPS)

```bash
git clone https://github.com/Rockenrol2017/swarm
cd swarm

# Сборка
go build -o swarm-node ./cmd/swarm-node/

# Конфиг
cat > /etc/swarm/node-config.json << EOF
{
  "mode": "bootstrap",
  "listen_addr": ":7437",
  "status_addr": ":19090",
  "identity_file": "/etc/swarm/identity.json"
}
EOF

# Запуск
sudo ./swarm-node -config /etc/swarm/node-config.json
```

### Client узел (домашний сервер)

```bash
cat > /etc/swarm/node-config.json << EOF
{
  "mode": "client",
  "bootstrap_addr": "IP_ВАШЕГО_VPS:7437",
  "socks5_addr": ":1090",
  "status_addr": ":19090",
  "identity_file": "/etc/swarm/identity.json",
  "traffic_file": "/var/lib/swarm/traffic.json"
}
EOF

sudo ./swarm-node -config /etc/swarm/node-config.json
```

### Оптимизация для спутника

```bash
sudo bash install/optimize-satellite.sh
```

Включает BBR, увеличивает TCP буферы до 16МБ, устанавливает DNS кэш dnsmasq.

---

## Архитектура

```
swarm/
├── cmd/
│   ├── swarm-node/        # Основной бинарник узла
│   └── swarm-monitor/     # Бинарник веб-дашборда
├── pkg/swarmnode/
│   ├── node.go            # Жизненный цикл, управление пирами
│   ├── peer.go            # QUIC соединения, relay форвардинг
│   ├── socks5.go          # Встроенный SOCKS5 прокси
│   ├── tproxy.go          # Прозрачный прокси (SO_TRANSPARENT)
│   ├── traffic.go         # Персистентные счётчики трафика
│   ├── latency.go         # RTT проба до bootstrap узла
│   └── status.go          # HTTP статус API
├── pkg/swarmproto/
│   ├── handshake.go       # Крипто рукопожатие: X25519 + Ed25519
│   ├── cipher.go          # Шифрование ChaCha20-Poly1305
│   └── packet.go          # Протокол передачи данных
├── swarm-monitor/
│   └── index.html         # Веб-дашборд UI
└── install/
    ├── optimize-satellite.sh  # BBR + настройка буферов
    ├── redeploy.sh            # Сборка и деплой
    └── systemd/               # Systemd unit файлы
```

---

## API

`GET /api/status` — статус узла

```json
{
  "mode": "client",
  "peers": 1,
  "bytes_up": 1234567,
  "bytes_down": 9876543,
  "bytes_today": 11111110,
  "bytes_month": 11111110,
  "limit_gb": 310,
  "limit_percent": 0.003,
  "latency_ms": 1450
}
```

`GET /health` — проверка доступности

`GET /api/peers` — список подключённых пиров

---

## Режимы узла

| Режим | Описание |
|-------|----------|
| `bootstrap` | Точка входа, принимает соединения, проксирует в интернет |
| `relay` | Форвардинг: Client → Relay → Bootstrap → Internet |
| `client` | Пользовательский узел, подключается к bootstrap или relay |

---

## Безопасность

- **ChaCha20-Poly1305** — аутентифицированное шифрование всего трафика
- **X25519** — эфемерный обмен ключами для каждой сессии
- **Ed25519** — подписи идентичности узла
- **Нет логов** — содержимое трафика никогда не записывается
- **Открытый код** — полный аудит возможен

Об уязвимостях сообщайте приватно — не через публичные Issues.
См. [SECURITY.md](SECURITY.md).

---

## Roadmap

- [x] QUIC транспорт с ChaCha20-Poly1305
- [x] Режимы bootstrap, relay, client
- [x] 2-hop relay форвардинг
- [x] Прозрачный прокси (TPROXY)
- [x] Мониторинг трафика (день/месяц)
- [x] RTT проба
- [x] Веб-дашборд
- [x] Оптимизация для спутника
- [ ] DHT peer discovery (без bootstrap)
- [ ] Android клиент
- [ ] Windows клиент
- [ ] Блокировка рекламы (DNS)
- [ ] Семейный режим

---

## Участие в разработке

Нужна помощь в направлениях:

- Клиент для Windows / macOS
- Приложение Android / iOS
- Улучшение веб-интерфейса
- Аудит безопасности
- Переводы и документация

См. [CONTRIBUTING.md](CONTRIBUTING.md).

---

## Лицензия

GNU General Public License v3.0 — см. [LICENSE](LICENSE)
