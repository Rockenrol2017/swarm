// swarm-monitor — мониторинг S.W.A.R.M. для домашнего сервера
// Читает статистику xray (swarm-out) + swarm-node API, отдаёт дашборд на :8081
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	xrayAPIAddr    = "127.0.0.1:10085"
	swarmAPIAddr   = "http://127.0.0.1:19090"
	webPort        = 8081
	pollInterval   = 3 * time.Second
	trafficLimitGB = 310.0 // SkyEdge: 310 ГБ/мес полной скорости (после — замедление)
)

var (
	startTime     = time.Now()
	mu            sync.RWMutex
	uplinkBytes   int64
	downlinkBytes int64
	xrayActive    atomic.Bool
	speedUp       atomic.Int64
	speedDown     atomic.Int64
	prevUplink    int64
	prevDownlink  int64
	prevTime      = time.Now()

	// swarm-node данные
	swarmMu        sync.RWMutex
	swarmNodeData  SwarmNodeStatus
	swarmPeersData []SwarmPeerStatus
)

// SwarmNodeStatus — ответ /api/status от swarm-node
type SwarmNodeStatus struct {
	Mode       string `json:"mode"`
	NodeID     string `json:"node_id"`
	Uptime     string `json:"uptime"`
	UptimeSecs int64  `json:"uptime_secs"`
	Peers      int    `json:"peers"`
	BytesSent  int64  `json:"bytes_sent"`
	BytesRecv  int64  `json:"bytes_recv"`
	Socks5Addr string `json:"socks5_addr"`
	TProxyAddr string `json:"tproxy_addr"`
	Bootstrap  string `json:"bootstrap"`

	// Traffic counters (SOCKS5 client-side, добавлены в v0.2)
	BytesUp      int64   `json:"bytes_up"`
	BytesDown    int64   `json:"bytes_down"`
	BytesToday   int64   `json:"bytes_today"`
	BytesMonth   int64   `json:"bytes_month"`
	LimitGB      float64 `json:"limit_gb"`
	LimitPercent float64 `json:"limit_percent"`
	LatencyMs    int64   `json:"latency_ms"` // RTT до VDS в мс, -1 = недоступен
}

// SwarmPeerStatus — один пир из /api/peers swarm-node
type SwarmPeerStatus struct {
	NodeID      string `json:"node_id"`
	ConnectedAt string `json:"connected_at"`
	UptimeSecs  int64  `json:"uptime_secs"`
	BytesSent   int64  `json:"bytes_sent"`
	BytesRecv   int64  `json:"bytes_recv"`
}

// Status — ответ /api/status монитора (для дашборда)
type Status struct {
	// Общее
	Status  string `json:"status"`
	Uptime  string `json:"uptime"`
	VDSNode string `json:"vds_node"`

	// xray
	XrayActive    bool    `json:"xray_active"`
	UplinkBytes   int64   `json:"uplink_bytes"`
	DownlinkBytes int64   `json:"downlink_bytes"`
	TotalBytes    int64   `json:"total_bytes"`
	SpeedUpBps    int64   `json:"speed_up_bps"`
	SpeedDownBps  int64   `json:"speed_down_bps"`
	TrafficLimitB int64   `json:"traffic_limit_bytes"`
	TrafficPct    float64 `json:"traffic_percent"`

	// swarm-node
	SwarmActive    bool              `json:"swarm_active"`
	SwarmMode      string            `json:"swarm_mode"`
	SwarmNodeID    string            `json:"swarm_node_id"`
	SwarmPeers     int               `json:"swarm_peers"`
	SwarmBytesSent int64             `json:"swarm_bytes_sent"`
	SwarmBytesRecv int64             `json:"swarm_bytes_recv"`
	SwarmUptime    string            `json:"swarm_uptime"`
	SwarmPeerList  []SwarmPeerStatus `json:"swarm_peer_list"`

	// Traffic counters (client-side SOCKS5, v0.2)
	SwarmBytesUp      int64   `json:"swarm_bytes_up"`
	SwarmBytesDown    int64   `json:"swarm_bytes_down"`
	SwarmBytesToday   int64   `json:"swarm_bytes_today"`
	SwarmBytesMonth   int64   `json:"swarm_bytes_month"`
	SwarmLimitGB      float64 `json:"swarm_limit_gb"`
	SwarmLimitPercent float64 `json:"swarm_limit_percent"`
	SwarmLatencyMs    int64   `json:"swarm_latency_ms"` // RTT до VDS
}

func main() {
	log.Printf("S.W.A.R.M. Monitor запущен на :%d", webPort)
	go pollLoop()
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/api/status", handleStatus)
	addr := fmt.Sprintf(":%d", webPort)
	localIP := getLocalIP()
	log.Printf("Дашборд: http://%s:%d", localIP, webPort)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}

func pollLoop() {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	poll()
	for range ticker.C {
		poll()
	}
}

func poll() {
	// xray статус
	cmd := exec.Command("systemctl", "is-active", "xray")
	out, err := cmd.Output()
	active := err == nil && strings.TrimSpace(string(out)) == "active"
	xrayActive.Store(active)

	if active {
		up := queryXrayStats("outbound>>>swarm-out>>>traffic>>>uplink")
		dn := queryXrayStats("outbound>>>swarm-out>>>traffic>>>downlink")
		mu.Lock()
		now := time.Now()
		dt := now.Sub(prevTime).Seconds()
		if dt > 0 {
			if dUp := up - prevUplink; dUp >= 0 {
				speedUp.Store(int64(float64(dUp) / dt))
			}
			if dDn := dn - prevDownlink; dDn >= 0 {
				speedDown.Store(int64(float64(dDn) / dt))
			}
		}
		uplinkBytes = up
		downlinkBytes = dn
		prevUplink = up
		prevDownlink = dn
		prevTime = now
		mu.Unlock()
	}

	// swarm-node статус
	pollSwarmNode()
}

func pollSwarmNode() {
	// GET /api/status
	resp, err := http.Get(swarmAPIAddr + "/api/status")
	if err != nil {
		swarmMu.Lock()
		swarmNodeData = SwarmNodeStatus{}
		swarmMu.Unlock()
		return
	}
	defer resp.Body.Close()

	var ns SwarmNodeStatus
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &ns); err != nil {
		return
	}

	// GET /api/peers
	var peers []SwarmPeerStatus
	resp2, err := http.Get(swarmAPIAddr + "/api/peers")
	if err == nil {
		defer resp2.Body.Close()
		body2, _ := io.ReadAll(resp2.Body)
		json.Unmarshal(body2, &peers)
	}

	swarmMu.Lock()
	swarmNodeData = ns
	swarmPeersData = peers
	swarmMu.Unlock()
}

func queryXrayStats(name string) int64 {
	cmd := exec.Command("xray", "api", "stats",
		"--server="+xrayAPIAddr, "--reset=false", "--name", name)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	var result struct {
		Stat struct {
			Value json.Number `json:"value"`
		} `json:"stat"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return 0
	}
	val, _ := result.Stat.Value.Int64()
	return val
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	up := uplinkBytes
	dn := downlinkBytes
	mu.RUnlock()

	swarmMu.RLock()
	swarm := swarmNodeData
	peers := swarmPeersData
	swarmMu.RUnlock()

	total := up + dn
	limitB := int64(trafficLimitGB * 1024 * 1024 * 1024)
	pct := float64(total) / float64(limitB) * 100

	overallStatus := "running"
	swarmActive := swarm.NodeID != ""
	if !xrayActive.Load() && !swarmActive {
		overallStatus = "down"
	}

	d := time.Since(startTime)
	uptimeStr := fmt.Sprintf("%dч %02dм", int(d.Hours()), int(d.Minutes())%60)

	// Укорачиваем NodeID для отображения
	shortID := swarm.NodeID
	if len(shortID) > 16 {
		shortID = shortID[:16]
	}

	resp := Status{
		Status:  overallStatus,
		Uptime:  uptimeStr,
		VDSNode: "Стокгольм 🇸🇪",

		XrayActive:    xrayActive.Load(),
		UplinkBytes:   up,
		DownlinkBytes: dn,
		TotalBytes:    total,
		SpeedUpBps:    speedUp.Load(),
		SpeedDownBps:  speedDown.Load(),
		TrafficLimitB: limitB,
		TrafficPct:    pct,

		SwarmActive:    swarmActive,
		SwarmMode:      swarm.Mode,
		SwarmNodeID:    shortID,
		SwarmPeers:     swarm.Peers,
		SwarmBytesSent: swarm.BytesSent,
		SwarmBytesRecv: swarm.BytesRecv,
		SwarmUptime:    swarm.Uptime,
		SwarmPeerList:  peers,

		SwarmBytesUp:      swarm.BytesUp,
		SwarmBytesDown:    swarm.BytesDown,
		SwarmBytesToday:   swarm.BytesToday,
		SwarmBytesMonth:   swarm.BytesMonth,
		SwarmLimitGB:      swarm.LimitGB,
		SwarmLimitPercent: swarm.LimitPercent,
		SwarmLatencyMs:    swarm.LatencyMs,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "/opt/swarm-monitor/index.html")
}

// getLocalIP возвращает первый не-loopback IPv4 адрес.
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "localhost"
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return "localhost"
}
