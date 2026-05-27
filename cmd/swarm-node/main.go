// cmd/swarm-node — демон узла роя S.W.A.R.M.
//
// Режимы запуска:
//   swarm-node -mode bootstrap   — bootstrap сервер (VDS)
//   swarm-node -mode relay       — relay узел (домашний сервер / VPS)
//   swarm-node -mode client      — клиент (подключается к bootstrap, экспортирует SOCKS5)
//
// Конфиг по умолчанию: /etc/swarm/node-config.json
// Переопределить: -config /path/to/config.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/narodnaya-set/swarm/pkg/swarmnode"
)

func ensureDirs() {
	// Создаём каталог для persistent данных (traffic.json, etc.)
	// Делаем это в начале — не ждём пока trafficStore сам создаст при первой записи.
	dirs := []string{"/var/lib/swarm"}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			log.Printf("[main] предупреждение: mkdir %s: %v", d, err)
		}
	}
}

func main() {
	// ─── Флаги ──────────────────────────────────────────────────────────────
	configPath := flag.String("config", "/etc/swarm/node-config.json", "путь к конфигу")
	modeFlag := flag.String("mode", "", "режим: client | relay | bootstrap (переопределяет конфиг)")
	flag.Parse()

	// Создаём необходимые каталоги заранее
	ensureDirs()

	// ─── Загрузка конфига ───────────────────────────────────────────────────
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("[main] Ошибка конфига: %v", err)
	}

	// Флаг -mode переопределяет конфиг
	if *modeFlag != "" {
		cfg.Mode = *modeFlag
	}

	if cfg.Mode == "" {
		log.Fatalf("[main] Укажите режим: client | relay | bootstrap (в конфиге или через -mode)")
	}

	// ─── Создание и запуск узла ─────────────────────────────────────────────
	node, err := swarmnode.New(cfg)
	if err != nil {
		log.Fatalf("[main] Ошибка создания узла: %v", err)
	}

	if err := node.Start(); err != nil {
		log.Fatalf("[main] Ошибка запуска: %v", err)
	}

	log.Printf("[main] S.W.A.R.M. node запущен. Режим: %s | NodeID: %s", cfg.Mode, node.NodeID()[:16])
	if cfg.Socks5Addr != "" {
		log.Printf("[main] SOCKS5 прокси: %s", cfg.Socks5Addr)
	}
	if cfg.TProxyAddr != "" {
		log.Printf("[main] TPROXY: %s", cfg.TProxyAddr)
	}
	if cfg.ListenAddr != "" && cfg.Mode != "client" {
		log.Printf("[main] QUIC listener: %s", cfg.ListenAddr)
	}
	if cfg.StatusAddr != "" {
		log.Printf("[main] HTTP статус: http://localhost%s/api/status", cfg.StatusAddr)
	}

	// ─── Ожидание сигнала ───────────────────────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh

	log.Printf("[main] Получен сигнал %s, останавливаем...", sig)
	node.Stop()
	log.Printf("[main] Остановлен.")
}

// loadConfig загружает конфиг из файла.
// Если файл не найден — возвращает дефолтный конфиг для быстрого старта.
func loadConfig(path string) (*swarmnode.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[main] Конфиг %s не найден, используем дефолтный", path)
			return defaultConfig(), nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg swarmnode.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Дефолты для незаполненных полей
	applyDefaults(&cfg)
	return &cfg, nil
}

// defaultConfig — конфиг по умолчанию для bootstrap узла на VDS.
// Используется когда файл конфига не найден.
func defaultConfig() *swarmnode.Config {
	return &swarmnode.Config{
		ListenAddr:   ":7437",
		Socks5Addr:   ":1090",
		IdentityFile: "/etc/swarm/identity.json",
		BootstrapAddr: "", // задаётся в конфиге: "YOUR_VDS_IP:7437"
		MaxPeers:     50,
		Mode:         "bootstrap",
	}
}

func applyDefaults(cfg *swarmnode.Config) {
	// ListenAddr: дефолт только для relay/bootstrap
	if cfg.ListenAddr == "" && (cfg.Mode == "relay" || cfg.Mode == "bootstrap") {
		cfg.ListenAddr = ":7437"
	}
	// Socks5Addr и TProxyAddr: пустая строка = выключено (не перезаписываем)
	if cfg.IdentityFile == "" {
		cfg.IdentityFile = "/etc/swarm/identity.json"
	}
	if cfg.MaxPeers == 0 {
		cfg.MaxPeers = 50
	}
	// StatusAddr: дефолт :19090 для всех режимов
	if cfg.StatusAddr == "" {
		cfg.StatusAddr = ":19090"
	}
	// TrafficFile: сохранение счётчиков трафика для мониторинга SkyEdge лимита.
	// По умолчанию только для client режима (на VDS это не нужно).
	if cfg.TrafficFile == "" && cfg.Mode == "client" {
		cfg.TrafficFile = "/var/lib/swarm/traffic.json"
	}
	// SkyEdge лимит по умолчанию: 310 ГБ/мес полной скорости.
	if cfg.SkyEdgeLimitGB == 0 && cfg.Mode == "client" {
		cfg.SkyEdgeLimitGB = 310.0
	}
	// MaxRelayPercent: сколько % канала отдаём рою.
	// Дефолт: 20% для client (экономия спутникового трафика), 100% для bootstrap/relay.
	if cfg.MaxRelayPercent == 0 {
		if cfg.Mode == "client" {
			cfg.MaxRelayPercent = 20
		} else {
			cfg.MaxRelayPercent = 100
		}
	}
	// Обратная совместимость: если только BootstrapAddr задан → добавляем в BootstrapAddrs
	if cfg.BootstrapAddr != "" {
		found := false
		for _, a := range cfg.BootstrapAddrs {
			if a == cfg.BootstrapAddr {
				found = true
				break
			}
		}
		if !found {
			cfg.BootstrapAddrs = append(cfg.BootstrapAddrs, cfg.BootstrapAddr)
		}
	}
}
