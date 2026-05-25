package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// StatusServer HTTP сервер для веб-дашборда.
type StatusServer struct {
	port      int
	startTime time.Time
	indexHTML []byte

	// Атомарные счётчики трафика (байты)
	bytesIn  atomic.Int64
	bytesOut atomic.Int64

	// Атомарный флаг статуса VDS подключения
	vdsConnected atomic.Bool

	// Для расчёта скорости (delta за последние 2с)
	prevBytesIn  int64
	prevBytesOut int64
	prevTime     time.Time

	// Текущая скорость (байт/с)
	speedIn  atomic.Int64
	speedOut atomic.Int64
}

// Status структура ответа /api/status
type Status struct {
	Status       string  `json:"status"`
	Uptime       string  `json:"uptime"`
	BytesIn      int64   `json:"bytes_in"`
	BytesOut     int64   `json:"bytes_out"`
	SpeedInBps   int64   `json:"speed_in_bps"`   // байт/с входящий
	SpeedOutBps  int64   `json:"speed_out_bps"`  // байт/с исходящий
	VDSConnected bool    `json:"vds_connected"`
	VDSNode      string  `json:"vds_node"`        // "Стокгольм 🇸🇪"
}

// New создаёт StatusServer.
// port — порт для HTTP сервера (8080).
// indexHTML — путь к index.html.
func New(port int, indexHTMLPath string) (*StatusServer, error) {
	html, err := os.ReadFile(indexHTMLPath)
	if err != nil {
		// Если файл не найден — используем встроенную страницу
		html = []byte(fallbackHTML)
	}

	return &StatusServer{
		port:      port,
		startTime: time.Now(),
		indexHTML: html,
		prevTime:  time.Now(),
	}, nil
}

// Start запускает HTTP сервер в горутине.
func (s *StatusServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)

	addr := fmt.Sprintf(":%d", s.port)
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			fmt.Printf("веб-сервер завершился: %v\n", err)
		}
	}()

	return nil
}

// AddBytes обновляет счётчики трафика.
// Вызывается из горутины мониторинга.
func (s *StatusServer) AddBytes(in, out int64) {
	s.bytesIn.Add(in)
	s.bytesOut.Add(out)
}

// UpdateSpeed пересчитывает скорость на основе delta.
// Вызывается каждые 2 секунды.
func (s *StatusServer) UpdateSpeed() {
	now := time.Now()
	dt := now.Sub(s.prevTime).Seconds()
	if dt <= 0 {
		return
	}

	curIn := s.bytesIn.Load()
	curOut := s.bytesOut.Load()

	deltaIn := curIn - s.prevBytesIn
	deltaOut := curOut - s.prevBytesOut

	s.speedIn.Store(int64(float64(deltaIn) / dt))
	s.speedOut.Store(int64(float64(deltaOut) / dt))

	s.prevBytesIn = curIn
	s.prevBytesOut = curOut
	s.prevTime = now
}

// SetVDSConnected обновляет статус подключения к VDS.
func (s *StatusServer) SetVDSConnected(connected bool) {
	s.vdsConnected.Store(connected)
}

// handleIndex отдаёт index.html
func (s *StatusServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.indexHTML)
}

// handleStatus отдаёт JSON статус
func (s *StatusServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(s.startTime)
	uptimeStr := formatUptime(uptime)

	status := Status{
		Status:       "running",
		Uptime:       uptimeStr,
		BytesIn:      s.bytesIn.Load(),
		BytesOut:     s.bytesOut.Load(),
		SpeedInBps:   s.speedIn.Load(),
		SpeedOutBps:  s.speedOut.Load(),
		VDSConnected: s.vdsConnected.Load(),
		VDSNode:      "Стокгольм 🇸🇪",
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(status)
}

// formatUptime форматирует длительность в строку "2ч 34м 15с"
func formatUptime(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60

	if h > 0 {
		return fmt.Sprintf("%dч %02dм %02dс", h, m, sec)
	}
	if m > 0 {
		return fmt.Sprintf("%dм %02dс", m, sec)
	}
	return fmt.Sprintf("%dс", sec)
}

// fallbackHTML показывается если index.html не найден
const fallbackHTML = `<!DOCTYPE html>
<html lang="ru">
<head><meta charset="utf-8"><title>S.W.A.R.M.</title>
<style>body{background:#1a1a2e;color:#eee;font-family:monospace;text-align:center;padding:50px}</style>
</head>
<body>
<h1>🟢 S.W.A.R.M.</h1>
<p>Защита активна</p>
<p><a href="/api/status" style="color:#00ff88">API статус</a></p>
</body>
</html>`
