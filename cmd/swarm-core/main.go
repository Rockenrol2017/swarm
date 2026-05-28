// S.W.A.R.M. Core — Этап 1
// Прозрачный прокси через VLESS + Reality
//
// Порядок запуска:
//  1. Загрузить /etc/swarm/config.json
//  2. Включить kill switch (DROP + masquerade + DNS защита)
//  3. Создать TUN интерфейс swarm0
//  4. Сгенерировать xray конфиг из шаблона
//  5. Запустить xray subprocess
//  6. Poll SOCKS5 127.0.0.1:1080 (timeout 30с)
//  7. Запустить tun2socks subprocess
//  8. Маршруты: VDS через DefaultGW | 0.0.0.0/0 через swarm0
//  9. Запустить HTTP :8080
// 10. Горутина мониторинга каждые 2с
// 11. SIGTERM/SIGINT: стоп процессов → восстановить маршруты → снять kill switch

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/narodnaya-set/swarm/internal/config"
	"github.com/narodnaya-set/swarm/internal/killswitch"
	"github.com/narodnaya-set/swarm/internal/proxy"
	"github.com/narodnaya-set/swarm/internal/tun"
	"github.com/narodnaya-set/swarm/internal/web"
)

const (
	configPath      = "/etc/swarm/config.json"
	xrayTemplatePath = "/etc/swarm/xray-template.json"
	xrayConfigPath  = "/tmp/swarm-xray-config.json"
	indexHTMLPath   = "/usr/share/swarm/index.html"
	version         = "0.1.0"
)

func main() {
	log.SetPrefix("[swarm] ")
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)

	log.Printf("S.W.A.R.M. Core v%s запускается...", version)

	// ─── Шаг 1: Загрузить конфиг ───────────────────────────────────────────
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("ОШИБКА: не удалось загрузить конфиг: %v", err)
	}
	log.Printf("Конфиг загружен: VDS=%s:%d, TUN=%s", cfg.VdsIP, cfg.VdsPort, cfg.TunName)

	// Проверяем root права (обязательны для iptables и TUN)
	if os.Getuid() != 0 {
		log.Fatal("ОШИБКА: требуются права root (sudo)")
	}

	// ─── Шаг 2: Kill switch ────────────────────────────────────────────────
	ks := killswitch.New(cfg.VdsIP, cfg.VdsPort, cfg.TunName, cfg.WebPort, cfg.DefaultIface)
	log.Println("Включаем kill switch...")
	if err := ks.Enable(); err != nil {
		log.Fatalf("ОШИБКА kill switch: %v", err)
	}
	log.Println("Kill switch активен — политика DROP")

	// Обязательная очистка при выходе
	defer func() {
		log.Println("Снимаем kill switch...")
		if err := ks.Disable(); err != nil {
			log.Printf("предупреждение: %v", err)
		}
	}()

	// ─── Шаг 3: TUN интерфейс ──────────────────────────────────────────────
	log.Printf("Создаём TUN интерфейс %s (%s)...", cfg.TunName, cfg.TunIP)
	tunDev, err := tun.New(cfg.TunName, cfg.TunIP)
	if err != nil {
		log.Fatalf("ОШИБКА TUN: %v", err)
	}
	defer func() {
		log.Printf("Удаляем TUN %s...", cfg.TunName)
		_ = tunDev.Close()
	}()

	// Устанавливаем MTU 1420 (учитывает VLESS/Reality overhead)
	if err := tunDev.SetMTU(1420); err != nil {
		log.Printf("предупреждение MTU: %v", err)
	}
	log.Printf("TUN %s поднят", cfg.TunName)

	// ─── Шаг 4-5: Запустить xray ───────────────────────────────────────────
	log.Println("Запускаем xray...")
	xrayProc, err := proxy.StartXray(xrayTemplatePath, xrayConfigPath, proxy.XrayConfig{
		VDSHost:    cfg.VdsIP,
		VDSPort:    cfg.VdsPort,
		UUID:       cfg.VlessUUID,
		SNI:        cfg.SNI,
		PublicKey:  cfg.PublicKey,
		ShortID:    cfg.ShortId,
		Socks5Port: cfg.Socks5Port,
	})
	if err != nil {
		log.Fatalf("ОШИБКА xray: %v", err)
	}
	defer func() {
		log.Println("Останавливаем xray...")
		_ = xrayProc.Stop()
	}()
	log.Printf("xray запущен (PID %d)", xrayProc.PID())

	// ─── Шаг 6: Ждём SOCKS5 ────────────────────────────────────────────────
	socks5Addr := fmt.Sprintf("127.0.0.1:%d", cfg.Socks5Port)
	log.Printf("Ожидаем SOCKS5 на %s...", socks5Addr)
	if err := proxy.WaitForSocks5(socks5Addr, 30*time.Second); err != nil {
		log.Fatalf("ОШИБКА: xray SOCKS5 не поднялся: %v", err)
	}
	log.Println("SOCKS5 готов")

	// ─── Шаг 7: tun2socks ──────────────────────────────────────────────────
	log.Printf("Запускаем tun2socks (%s → %s)...", cfg.TunName, socks5Addr)
	t2s, err := proxy.StartTun2Socks(cfg.TunName, socks5Addr)
	if err != nil {
		log.Fatalf("ОШИБКА tun2socks: %v", err)
	}
	defer func() {
		log.Println("Останавливаем tun2socks...")
		_ = t2s.Stop()
	}()
	log.Println("tun2socks запущен")

	// ─── Шаг 8: Маршруты ───────────────────────────────────────────────────
	log.Println("Настраиваем маршруты...")
	if err := setupRoutes(cfg); err != nil {
		log.Fatalf("ОШИБКА маршрутизации: %v", err)
	}
	defer func() {
		log.Println("Восстанавливаем маршруты...")
		cleanupRoutes(cfg)
	}()
	log.Printf("Маршруты настроены: VDS=%s → %s, default → %s",
		cfg.VdsIP, cfg.DefaultGW, cfg.TunName)

	// ─── Шаг 9: Веб-сервер ─────────────────────────────────────────────────
	webSrv, err := web.New(cfg.WebPort, indexHTMLPath)
	if err != nil {
		log.Fatalf("ОШИБКА веб-сервер: %v", err)
	}
	if err := webSrv.Start(); err != nil {
		log.Fatalf("ОШИБКА запуска веб-сервера: %v", err)
	}
	log.Printf("Веб-интерфейс: http://localhost:%d", cfg.WebPort)

	// ─── Шаг 10: Горутина мониторинга ──────────────────────────────────────
	go monitorLoop(xrayProc, t2s, webSrv)

	// Финальное сообщение
	log.Printf("✓ S.W.A.R.M. запущен! Весь трафик → %s (Стокгольм 🇸🇪)", cfg.VdsIP)
	log.Printf("  Веб-дашборд: http://localhost:%d", cfg.WebPort)

	// ─── Шаг 11: Ожидаем сигнал завершения ─────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	log.Printf("Получен сигнал %v, завершаем...", sig)
	// defer'ы выполнятся автоматически в обратном порядке
}

// setupRoutes настраивает маршруты:
// - VDS IP идёт через оригинальный шлюз (иначе петля!)
// - Весь остальной трафик через TUN
func setupRoutes(cfg *config.Config) error {
	// КРИТИЧНО: VDS трафик должен идти через оригинальный шлюз
	// Иначе: xray → TUN → xray → TUN → ... бесконечная петля
	if err := run("ip", "route", "add", cfg.VdsIP+"/32",
		"via", cfg.DefaultGW, "dev", cfg.DefaultIface); err != nil {
		return fmt.Errorf("маршрут VDS: %w", err)
	}

	// Весь остальной трафик через TUN
	if err := run("ip", "route", "add", "default", "dev", cfg.TunName,
		"metric", "1"); err != nil {
		return fmt.Errorf("маршрут default: %w", err)
	}

	return nil
}

// cleanupRoutes удаляет маршруты добавленные нами.
func cleanupRoutes(cfg *config.Config) {
	_ = run("ip", "route", "del", cfg.VdsIP+"/32")
	_ = run("ip", "route", "del", "default", "dev", cfg.TunName, "metric", "1")
}

// monitorLoop проверяет состояние процессов каждые 2 секунды.
func monitorLoop(xrayProc *proxy.XrayProcess, t2s *proxy.Tun2SocksProcess, webSrv *web.StatusServer) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		xrayOK := xrayProc.IsRunning()
		t2sOK  := t2s.IsRunning()

		webSrv.SetVDSConnected(xrayOK)
		webSrv.UpdateSpeed()

		if !xrayOK {
			log.Println("ПРЕДУПРЕЖДЕНИЕ: xray не работает!")
		}
		if !t2sOK {
			log.Println("ПРЕДУПРЕЖДЕНИЕ: tun2socks не работает!")
		}
	}
}

// run выполняет системную команду.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w\n%s", name, args, err, string(out))
	}
	return nil
}
