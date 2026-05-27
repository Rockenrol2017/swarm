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
	BytesUp   int64 `json:"bytes_up"`
	BytesDown int64 `json:"bytes_down"`

	// Накопленный трафик (persistent, выживает рестарты)
	BytesToday   int64   `json:"bytes_today"`
	BytesMonth   int64   `json:"bytes_month"`
	LimitGB      float64 `json:"limit_gb,omitempty"`
	LimitPercent float64 `json:"limit_percent,omitempty"`

	// RTT до bootstrap сервера
	LatencyMs int64 `json:"latency_ms"`

	// Пропускная способность канала (bandwidth.go)
	ChannelRxMbps float64 `json:"channel_rx_mbps,omitempty"`
	ChannelTxMbps float64 `json:"channel_tx_mbps,omitempty"`
	ChannelLoadPct float64 `json:"channel_load_pct,omitempty"`
	RelayActive   bool    `json:"relay_active,omitempty"`

	// Гео-статистика (geo.go)
	PeersByCountry map[string]int `json:"peers_by_country,omitempty"`

	// DHT: количество известных пиров в кэше
	CachedPeers int `json:"cached_peers,omitempty"`

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
	Country     string `json:"country,omitempty"`
	IPType      string `json:"ip_type,omitempty"`
	LatencyMs   int64  `json:"latency_ms,omitempty"`
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

	// Гео-статистика: считаем пиров по странам
	peersByCountry := make(map[string]int)
	n.mu.RLock()
	for _, p := range n.peers {
		if p.Country != "" {
			peersByCountry[p.Country]++
		}
	}
	n.mu.RUnlock()

	// Статистика канала
	rxMbps, txMbps, loadPct := n.bw.Stats()

	// DHT кэш
	cachedPeers := 0
	if n.peerCache != nil {
		cachedPeers = n.peerCache.Len()
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
		CachedPeers:  cachedPeers,
	}
	if n.cfg.SkyEdgeLimitGB > 0 {
		status.LimitGB = n.cfg.SkyEdgeLimitGB
		status.LimitPercent = limitPercent
	}
	status.LatencyMs = n.latencyMs.Load()
	if rxMbps > 0 || txMbps > 0 {
		status.ChannelRxMbps = rxMbps
		status.ChannelTxMbps = txMbps
		status.ChannelLoadPct = loadPct
	}
	if len(peersByCountry) > 0 {
		status.PeersByCountry = peersByCountry
	}
	// relay_active = true если обратный туннель активен
	status.RelayActive = n.cfg.Mode == "client" && peerCount > 0

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
			Country:     p.Country,
			IPType:      p.IPType,
			LatencyMs:   p.LatencyMs.Load(),
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
