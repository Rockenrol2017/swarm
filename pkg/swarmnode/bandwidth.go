//go:build linux

package swarmnode

// bandwidth.go — мониторинг пропускной способности канала (только Linux).
//
// Читает /proc/net/dev каждые 5 секунд и вычисляет скорость в Мбит/с.
// Используется обратным туннелем (reverse_tunnel.go) для принятия решения
// о том, принимать ли чужой трафик (relay): если канал > 80% занят → отказ.
//
// Автоматически определяет основной сетевой интерфейс (первый не-loopback).

import (
	"bufio"
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BandwidthMonitor — монитор пропускной способности.
type BandwidthMonitor struct {
	mu          sync.RWMutex
	iface       string  // имя интерфейса ("eth0", "enp3s0", ...)
	rxMbps      float64 // текущая скорость приёма (Мбит/с)
	txMbps      float64 // текущая скорость передачи (Мбит/с)
	loadPercent float64 // % использования от maxMbps
	maxMbps     float64 // максимальная скорость канала (для расчёта %)

	// Предыдущие значения байт из /proc/net/dev
	prevRxBytes uint64
	prevTxBytes uint64
	prevTime    time.Time
}

// newBandwidthMonitor создаёт монитор.
func newBandwidthMonitor() *BandwidthMonitor {
	return &BandwidthMonitor{
		maxMbps: 100, // 100 Мбит/с дефолт (будет уточнён при первом замере)
	}
}

// start запускает фоновое измерение.
func (bw *BandwidthMonitor) start(ctx context.Context) {
	iface := detectInterface()
	if iface == "" {
		log.Printf("[bw] интерфейс не найден, мониторинг отключён")
		return
	}

	bw.mu.Lock()
	bw.iface = iface
	bw.mu.Unlock()

	log.Printf("[bw] мониторинг интерфейса %s", iface)

	// Первое чтение для инициализации prev значений
	rx, tx, err := readIfaceBytes(iface)
	if err != nil {
		log.Printf("[bw] ошибка чтения /proc/net/dev: %v", err)
		return
	}
	bw.mu.Lock()
	bw.prevRxBytes = rx
	bw.prevTxBytes = tx
	bw.prevTime = time.Now()
	bw.mu.Unlock()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			bw.update()
		}
	}
}

// update читает текущие счётчики и вычисляет скорость.
func (bw *BandwidthMonitor) update() {
	bw.mu.Lock()
	defer bw.mu.Unlock()

	rx, tx, err := readIfaceBytes(bw.iface)
	if err != nil {
		return
	}

	now := time.Now()
	dt := now.Sub(bw.prevTime).Seconds()
	if dt <= 0 {
		return
	}

	// delta bytes / время * 8 / 1e6 = Мбит/с
	rxMbps := float64(rx-bw.prevRxBytes) * 8 / 1e6 / dt
	txMbps := float64(tx-bw.prevTxBytes) * 8 / 1e6 / dt

	bw.rxMbps = rxMbps
	bw.txMbps = txMbps
	bw.prevRxBytes = rx
	bw.prevTxBytes = tx
	bw.prevTime = now

	// Загрузка = max(rx, tx) / maxMbps * 100
	maxCurrent := rxMbps
	if txMbps > maxCurrent {
		maxCurrent = txMbps
	}
	bw.loadPercent = maxCurrent / bw.maxMbps * 100
	if bw.loadPercent > 100 {
		// Если превышаем дефолт — обновляем maxMbps (авто-калибровка)
		bw.maxMbps = maxCurrent * 1.2
		bw.loadPercent = 100
	}
}

// CurrentLoadPercent возвращает текущую загрузку канала в %.
// 0-100: 0 = канал свободен, 100 = канал полностью занят.
func (bw *BandwidthMonitor) CurrentLoadPercent() float64 {
	bw.mu.RLock()
	defer bw.mu.RUnlock()
	return bw.loadPercent
}

// Stats возвращает текущую статистику (rx, tx Мбит/с, % загрузки).
func (bw *BandwidthMonitor) Stats() (rxMbps, txMbps, loadPct float64) {
	bw.mu.RLock()
	defer bw.mu.RUnlock()
	return bw.rxMbps, bw.txMbps, bw.loadPercent
}

// detectInterface определяет основной сетевой интерфейс.
// Возвращает первый не-loopback и не-virtual интерфейс из /proc/net/dev.
func detectInterface() string {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.Contains(line, ":") {
			continue
		}
		iface := strings.TrimSpace(strings.SplitN(line, ":", 2)[0])
		// Пропускаем loopback и виртуальные
		if iface == "lo" || strings.HasPrefix(iface, "docker") ||
			strings.HasPrefix(iface, "veth") || strings.HasPrefix(iface, "br-") ||
			strings.HasPrefix(iface, "virbr") {
			continue
		}
		return iface
	}
	return ""
}

// readIfaceBytes читает rx/tx байты для интерфейса из /proc/net/dev.
func readIfaceBytes(iface string) (rx, tx uint64, err error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, iface+":") {
			continue
		}

		// Формат: "  eth0:  rxbytes  rxpkts  rxerr ... txbytes ..."
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}

		rxBytes, _ := strconv.ParseUint(fields[0], 10, 64)
		txBytes, _ := strconv.ParseUint(fields[8], 10, 64)
		return rxBytes, txBytes, nil
	}

	return 0, 0, nil
}
