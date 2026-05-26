# S.W.A.R.M.
### Secure · Worldwide · Anonymous · Routing · Mesh

> Децентрализованная P2P зашифрованная меш-сеть.  
> **Чем больше узлов — тем быстрее и надёжнее сеть для всех.**

[![Release](https://img.shields.io/github/v/release/Rockenrol2017/swarm?color=brightgreen)](https://github.com/Rockenrol2017/swarm/releases/latest)
[![Build](https://img.shields.io/github/actions/workflow/status/Rockenrol2017/swarm/release.yml?label=build)](https://github.com/Rockenrol2017/swarm/actions)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.22+-blue.svg)](https://golang.org)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

---

## 🌍 Запусти узел — помоги сети

> **Есть VPS? Одна команда — и ты часть роя.**

Каждый bootstrap узел делает всю сеть быстрее и надёжнее для каждого участника.  
Никакой настройки. Никакого обслуживания. Запустил и забыл.

```bash
curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install.sh | bash
```

**Требования:** Linux VPS (любой хостинг) · Root доступ · Порт 7437/UDP открыт · ~50 МБ RAM

Скрипт автоматически скачивает готовый бинарник, настраивает systemd и открывает порты.  
Через ~1 минуту твой узел работает и обслуживает рой. 🎉

> 💡 **Хочешь собрать из исходников?**
> ```bash
> curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install/setup-bootstrap.sh | bash
> ```

> 💡 **Чем разнообразнее география узлов — тем лучше.**  
> Франкфурт, Хельсинки, Сингапур, Нью-Йорк — каждая локация важна.

---

## Что это

S.W.A.R.M. — самохостируемая децентрализованная меш-сеть на Go.
Создаёт зашифрованный туннель между вашими устройствами и выходными узлами
через QUIC транспорт и шифрование ChaCha20-Poly1305.

Каждый участник усиливает сеть. Нет центральных серверов.
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
- **Прозрачный прокси** — перехватывает трафик на уровне ОС (TPROXY, только Linux)
- **3 режима узла** — bootstrap, relay, client
- **Мониторинг трафика** — счётчики день/месяц, сохраняются при перезагрузке
- **RTT проба** — мониторинг качества туннеля каждые 30 секунд
- **Веб-дашборд** — реальная статистика на порту 8081
- **Оптимизация для спутника** — BBR, большие TCP буферы, DNS кэш
- **Открытый код** — GPL v3, проверьте всё сами

---

## Быстрый старт

### Bootstrap узел (VPS) — одна команда

```bash
curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install.sh | bash
```

Go не нужен — скачивается готовый бинарник, systemd настраивается автоматически.

### Client узел (домашний сервер)

```bash
# Установка
curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install.sh | bash

# Редактируем конфиг
nano /etc/swarm/node-config.json
# Укажи: "mode": "client", "bootstrap_addr": "IP_ВАШЕГО_VPS:7437"

# Перезапуск
systemctl restart swarm-node
```

### Проверка

```bash
# Статус
curl http://localhost:19090/api/status

# Логи
journalctl -u swarm-node -f
```

---

## Архитектура

```
swarm/
├── cmd/swarm-node/        # Основной бинарник
├── pkg/swarmnode/
│   ├── node.go            # Жизненный цикл, управление пирами
│   ├── peer.go            # QUIC соединения, keepalive
│   ├── socks5.go          # Встроенный SOCKS5 прокси
│   ├── tproxy.go          # Прозрачный прокси (только Linux)
│   ├── traffic.go         # Персистентные счётчики трафика
│   └── status.go          # HTTP статус API :19090
├── pkg/swarmproto/
│   ├── handshake.go       # X25519 + Ed25519 рукопожатие
│   ├── cipher.go          # ChaCha20-Poly1305
│   └── packet.go          # Протокол фреймов
└── install/
    ├── install.sh             # One-liner установка
    ├── setup-bootstrap.sh     # Настройка нового VDS
    ├── update-vds.sh          # Обновление существующего VDS
    └── systemd/               # Systemd unit файлы
```

---

## API

`GET /api/status` — статус узла

```json
{
  "mode": "client",
  "node_id": "...",
  "uptime": "2h34m",
  "peers": 1,
  "bytes_up": 1234567,
  "bytes_down": 9876543,
  "bytes_today": 11111110,
  "bytes_month": 11111110,
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
- [x] Прозрачный прокси (TPROXY)
- [x] Мониторинг трафика (день/месяц, персистентный)
- [x] Готовые бинарники для всех платформ (CI/CD)
- [x] Оптимизация для спутниковых каналов
- [ ] DHT peer discovery (без bootstrap)
- [ ] Android клиент
- [ ] Windows / macOS клиент с GUI
- [ ] Блокировка рекламы (DNS)

---

## Участие в разработке

Нужна помощь:

- Клиент для Windows / macOS / Android / iOS
- Улучшение веб-интерфейса
- Аудит безопасности
- Переводы и документация

См. [CONTRIBUTING.md](CONTRIBUTING.md).

---

## Лицензия

GNU General Public License v3.0 — см. [LICENSE](LICENSE)
