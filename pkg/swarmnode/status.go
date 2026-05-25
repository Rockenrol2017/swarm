package swarmnode

// status.go — HTTP статус API для swarm-node.
//
// Эндпоинты:
//   GET /api/status  — общая информация об узле
//   GET /api/peers   — список подключённых пиров
//
// Используется swarm-monitor и другими инструментами мониторинга.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// NodeStatus — ответ /api/status.
type NodeStatus struct {
	Mode       string `json:"mode"`
	NodeID     string `json:"node_id"`
	Uptime     string `json:"uptime"`
	UptimeSecs int64  `json:"uptime_secs"`
	Peers      int    `json:"peers"`

	// Байты через QUIC (peer-level, считаются на bootstrap стороне)
	BytesSent int64 `json:"bytes_sent"`
	BytesRecv int64 `json:"bytes_recv"`

	// Байты через SOCKS5 (client-level, считаются на client стороне)
	// bytes_up = upload (клиент→рой), bytes_down = download (рой→клиент)
	BytesUp   int64 `json:"bytes_up"`
	BytesDown int64 `json:"bytes_down"`

	// Накопленный трафик (persistent, выживает рестарты).
	// Для мониторинга лимита SkyEdge (35-64 ГБ/мес).
	BytesToday   int64   `json:"bytes_today"`
	BytesMonth   int64   `json:"bytes_month"`
	LimitGB      float64 `json:"limit_gb,omitempty"`      // лимит в ГБ (0 = не настроен)
	LimitPercent float64 `json:"limit_percent,omitempty"` // % использования лимита

	// RTT до bootstrap сервера (мониторинг качества спутникового канала).
	// 0 = ещё не измерено, -1 = недоступен, >0 = RTT в миллисекундах.
	LatencyMs int64 `json:"latency_ms"`

	Socks5Addr string `json:"socks5_addr,omitempty"`
	TProxyAddr string `json:"tproxy_addr,omitempty"`
	ListenAddr string `json:"listen_addr,omitempty"`
	Bootstrap  string `json:"bootstrap,omitempty"`
}

// PeerStatus — один пир в ответе /api/peers.
type PeerStatus struct {
	NodeID      string `json:"node_id"`
	ConnectedAt string `json:"connected_at"`
	UptimeSecs  int64  `json:"uptime_secs"`
	BytesSent   int64  `json:"bytes_sent"`
	BytesRecv   int64  `json:"bytes_recv"`
}

// startStatusServer запускает HTTP сервер статуса.
func (n *Node) startStatusServer(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", n.handleStatus)
	mux.HandleFunc("/api/peers", n.handlePeers)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	log.Printf("[status] HTTP API на %s", addr)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		<-n.ctx.Done()
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("[status] HTTP ошибка: %v", err)
	}
}

func (n *Node) handleStatus(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(n.startTime)
	uptimeStr := formatDuration(uptime)

	// Суммируем байты по всем QUIC пирам (bootstrap-side счётчик)
	var totalSent, totalRecv int64
	n.mu.RLock()
	peerCount := len(n.peers)
	for _, p := range n.peers {
		totalSent += p.BytesSent()
		totalRecv += p.BytesRecv()
	}
	n.mu.RUnlock()

	// Persistent traffic counters (SkyEdge мониторинг + up/down направление).
	// Все значения выживают рестарт — хранятся в traffic.json.
	var bytesUp, bytesDown, bytesToday, bytesMonth int64
	if n.traffic != nil {
		tr := n.traffic.get()
		bytesUp = tr.BytesUp
		bytesDown = tr.BytesDown
		bytesToday = tr.BytesToday
		bytesMonth = tr.BytesMonth
	}

	// Процент использования лимита SkyEdge
	var limitPercent float64
	if n.cfg.SkyEdgeLimitGB > 0 && bytesMonth > 0 {
		limitPercent = float64(bytesMonth) / (1024 * 1024 * 1024) / n.cfg.SkyEdgeLimitGB * 100
	}

	status := NodeStatus{
		Mode:         n.cfg.Mode,
		NodeID:       hex.EncodeToString(n.identity.PubKey),
		Uptime:       uptimeStr,
		UptimeSecs:   int64(uptime.Seconds()),
		Peers:        peerCount,
		BytesSent:    totalSent,
		BytesRecv:    totalRecv,
		BytesUp:      bytesUp,
		BytesDown:    bytesDown,
		BytesToday:   bytesToday,
		BytesMonth:   bytesMonth,
		Socks5Addr:   n.cfg.Socks5Addr,
		TProxyAddr:   n.cfg.TProxyAddr,
		ListenAddr:   n.cfg.ListenAddr,
		Bootstrap:    n.cfg.BootstrapAddr,
	}
	if n.cfg.SkyEdgeLimitGB > 0 {
		status.LimitGB = n.cfg.SkyEdgeLimitGB
		status.LimitPercent = limitPercent
	}
	status.LatencyMs = n.latencyMs.Load()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(status)
}

func (n *Node) handlePeers(w http.ResponseWriter, r *http.Request) {
	n.mu.RLock()
	result := make([]PeerStatus, 0, len(n.peers))
	for _, p := range n.peers {
		uptime := time.Since(p.ConnectedAt())
		result = append(result, PeerStatus{
			NodeID:      p.NodeIDShort(),
			ConnectedAt: p.ConnectedAt().Format(time.RFC3339),
			UptimeSecs:  int64(uptime.Seconds()),
			BytesSent:   p.BytesSent(),
			BytesRecv:   p.BytesRecv(),
		})
	}
	n.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(result)
}

// formatDuration форматирует duration в читаемый вид.
func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dч %02dм", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dм %02dс", m, s)
	}
	return fmt.Sprintf("%dс", s)
}
