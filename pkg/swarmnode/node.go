// Package swarmnode — узел сети S.W.A.R.M.
// Каждый пользователь = один узел. Узел:
//   - Имеет уникальную идентичность (Ed25519 keypair)
//   - Подключается к bootstrap серверу (VDS)
//   - Принимает трафик от домашних устройств
//   - Маршрутизирует через рой
package swarmnode

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/narodnaya-set/swarm/pkg/swarmproto"
	"github.com/quic-go/quic-go"
)

// Config — конфигурация узла.
type Config struct {
	// Локальные настройки
	ListenAddr   string `json:"listen_addr"`   // ":7437" — QUIC порт
	Socks5Addr   string `json:"socks5_addr"`   // ":1090" — SOCKS5 для браузеров
	TProxyAddr   string `json:"tproxy_addr"`   // ":12346" — прозрачный прокси (нужен root)
	StatusAddr   string `json:"status_addr"`   // ":19090" — HTTP статус API
	IdentityFile string `json:"identity_file"` // путь к файлу с keypair

	// Bootstrap серверы (можно несколько)
	BootstrapAddr    string   `json:"bootstrap_addr"`     // основной: "YOUR_VDS_IP:7437"
	BootstrapAddrs   []string `json:"bootstrap_addrs"`    // дополнительные relay/bootstrap узлы
	BootstrapNodeID  string   `json:"bootstrap_node_id"`  // hex Ed25519 pubkey (опционально)

	// Параметры сети
	MaxPeers int    `json:"max_peers"` // 50
	Mode     string `json:"mode"`      // "client" | "relay" | "bootstrap"

	// Мониторинг трафика (SkyEdge лимит)
	TrafficFile    string  `json:"traffic_file"`     // путь для сохранения счётчиков (/var/lib/swarm/traffic.json)
	SkyEdgeLimitGB float64 `json:"skyedge_limit_gb"` // лимит в ГБ (0 = отключено)

	// Обратный туннель: сколько % пропускной способности отдавать роям.
	// 0 = дефолт (20% для client, 100% для bootstrap).
	MaxRelayPercent int `json:"max_relay_percent"`
}

// Node — узел роя.
type Node struct {
	cfg      *Config
	identity *swarmproto.NodeIdentity

	mu    sync.RWMutex
	peers map[[32]byte]*Peer // nodeID → peer

	// Round-robin счётчик для выбора пира (fallback когда нет latency данных)
	peerIdx uint64 // atomic

	// Persistent traffic accounting: выживает рестарты.
	// Хранит bytes_up, bytes_down (сегодня), bytes_today, bytes_month.
	// Используется для мониторинга лимита SkyEdge.
	traffic *trafficStore

	// RTT до bootstrap в миллисекундах (HTTP probe, latency.go).
	// 0 = не измерено, -1 = недоступен, >0 = RTT.
	// Обновляется runLatencyProbe каждые 30с.
	latencyMs atomic.Int64

	// peerCache — персистентный кэш известных пиров (DHT-заглушка).
	// Позволяет рою работать без bootstrap — загружает peers.json при старте.
	peerCache *PeerCache

	// bw — монитор пропускной способности канала (bandwidth.go).
	// Используется обратным туннелем для принятия решения о relay.
	bw *BandwidthMonitor

	// QUIC listener (только для relay/bootstrap режима)
	listener *quic.Listener

	startTime time.Time
	ctx       context.Context
	cancel    context.CancelFunc
}

// New создаёт узел из конфига.
func New(cfg *Config) (*Node, error) {
	id, err := loadOrGenerateIdentity(cfg.IdentityFile)
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Инициализируем peer cache — загружаем с диска если есть
	cache := newPeerCache("/var/lib/swarm/peers.json", 500)
	if err := cache.Load(); err != nil {
		log.Printf("[dht] пустой кэш пиров: %v", err)
	} else {
		log.Printf("[dht] загружено %d пиров из кэша", cache.Len())
	}

	return &Node{
		cfg:       cfg,
		identity:  id,
		peers:     make(map[[32]byte]*Peer),
		startTime: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
		traffic:   newTrafficStore(cfg.TrafficFile),
		peerCache: cache,
		bw:        newBandwidthMonitor(),
	}, nil
}

// NodeID возвращает hex-encoded ID узла.
func (n *Node) NodeID() string {
	return hex.EncodeToString(n.identity.PubKey)
}

// Start запускает узел.
func (n *Node) Start() error {
	log.Printf("[node] Запуск узла %s... (режим: %s)", n.NodeID()[:16], n.cfg.Mode)

	// Запускаем монитор пропускной способности (Linux: читает /proc/net/dev)
	go n.bw.start(n.ctx)

	// Периодическое сохранение peer cache на диск
	go n.peerCache.startPeriodicSave(n.ctx)

	switch n.cfg.Mode {
	case "bootstrap":
		// Bootstrap: только принимает входящие соединения
		if err := n.startListener(); err != nil {
			return fmt.Errorf("listener: %w", err)
		}
		go n.acceptLoop()

	case "relay":
		// Relay: принимает входящие И подключается к upstream (bootstrap/другой relay).
		// Трафик от клиентов форвардится через upstream а не диалится напрямую.
		if err := n.startListener(); err != nil {
			return fmt.Errorf("listener: %w", err)
		}
		go n.acceptLoop()
		go n.connectToBootstrapOrCache() // upstream для форвардинга

	case "client":
		// Клиент: только исходящие соединения к bootstrap/relay
		go n.connectToBootstrapOrCache()
	}

	// SOCKS5 прокси для браузеров/приложений
	if n.cfg.Socks5Addr != "" {
		go n.startSocks5()
	}

	// TPROXY — прозрачный перехват для всех домашних устройств (требует root)
	if n.cfg.TProxyAddr != "" {
		go n.startTProxy(n.cfg.TProxyAddr)
	}

	// Периодический обмен списком пиров (bootstrap/relay)
	if n.cfg.Mode == "bootstrap" || n.cfg.Mode == "relay" {
		go n.startPeerExchange()
	}

	// HTTP статус API
	if n.cfg.StatusAddr != "" {
		go n.startStatusServer(n.cfg.StatusAddr)
	}

	// Периодическое сохранение счётчиков трафика (каждые 60с)
	if n.traffic != nil && n.cfg.TrafficFile != "" {
		go n.runTrafficSaver()
	}

	// Измерение RTT до bootstrap (мониторинг качества спутникового канала).
	// Запускаем только для client — у bootstrap нет "upstream" для пинга.
	if n.cfg.BootstrapAddr != "" && n.cfg.Mode == "client" {
		go n.runLatencyProbe()
	}

	return nil
}

// Stop останавливает узел.
func (n *Node) Stop() {
	n.cancel()
	if n.listener != nil {
		n.listener.Close()
	}
	n.mu.Lock()
	for _, p := range n.peers {
		p.Close()
	}
	n.mu.Unlock()
	// Сохраняем счётчики трафика перед выходом
	if n.traffic != nil {
		n.traffic.save()
	}
	// Сохраняем peer cache перед выходом
	if n.peerCache != nil {
		if err := n.peerCache.Save(); err != nil {
			log.Printf("[dht] ошибка сохранения peer cache: %v", err)
		}
	}
	log.Printf("[node] Остановлен")
}

// Peers возвращает список подключённых пиров.
func (n *Node) Peers() []*Peer {
	n.mu.RLock()
	defer n.mu.RUnlock()
	peers := make([]*Peer, 0, len(n.peers))
	for _, p := range n.peers {
		peers = append(peers, p)
	}
	return peers
}

// PeerCount возвращает количество подключённых пиров.
func (n *Node) PeerCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.peers)
}

// selectPeer выбирает пир с минимальной задержкой (latency-first).
// Fallback: round-robin если latency не измерена.
// Используется SOCKS5/TPROXY на client-узле.
func (n *Node) selectPeer() *Peer {
	n.mu.RLock()
	peers := make([]*Peer, 0, len(n.peers))
	for _, p := range n.peers {
		if !p.IsClosed() {
			peers = append(peers, p)
		}
	}
	n.mu.RUnlock()

	if len(peers) == 0 {
		return nil
	}
	if len(peers) == 1 {
		return peers[0]
	}

	// Latency-first: выбираем пир с наименьшим RTT
	var best *Peer
	var bestLat int64 = 1<<62
	for _, p := range peers {
		if lat := p.LatencyMs.Load(); lat > 0 && lat < bestLat {
			bestLat = lat
			best = p
		}
	}
	if best != nil {
		return best
	}

	// Fallback: round-robin если latency ещё не измерена
	idx := atomic.AddUint64(&n.peerIdx, 1)
	return peers[int(idx)%len(peers)]
}

// selectPeerForDomain выбирает оптимальный пир для заданного домена.
// Для гео-ограниченного контента — пир с нужной страной/типом.
// Fallback: обычный selectPeer().
func (n *Node) selectPeerForDomain(domain string) *Peer {
	rules := geoRulesForDomain(domain)
	if len(rules) == 0 {
		return n.selectPeer()
	}

	n.mu.RLock()
	peers := make([]*Peer, 0, len(n.peers))
	for _, p := range n.peers {
		if !p.IsClosed() {
			peers = append(peers, p)
		}
	}
	n.mu.RUnlock()

	// Ищем пир, удовлетворяющий хотя бы одному правилу
	for _, rule := range rules {
		for _, p := range peers {
			switch rule {
			case "residential":
				if p.IPType == "residential" {
					return p
				}
			default:
				// rule = страна ("US", "GB", "DE", ...)
				if p.Country == rule {
					return p
				}
			}
		}
	}

	// Fallback: обычный выбор
	return n.selectPeer()
}

// selectUpstreamPeer выбирает upstream пир (только исходящие соединения).
// Используется relay режимом в forwardThroughUpstream: нужен именно upstream
// (bootstrap или другой relay), а не клиентский пир который подключился к нам.
// Это предотвращает цикл: relay→client вместо relay→bootstrap.
func (n *Node) selectUpstreamPeer() *Peer {
	n.mu.RLock()
	upstreams := make([]*Peer, 0, len(n.peers))
	for _, p := range n.peers {
		if p.isOutgoing && !p.IsClosed() {
			upstreams = append(upstreams, p)
		}
	}
	n.mu.RUnlock()

	if len(upstreams) == 0 {
		return nil
	}
	if len(upstreams) == 1 {
		return upstreams[0]
	}

	idx := atomic.AddUint64(&n.peerIdx, 1)
	return upstreams[int(idx)%len(upstreams)]
}

// addProxiedBytes добавляет байты к счётчикам трафика (SOCKS5 client side).
// up = байты от клиента к рою (upload), down = байты от роя к клиенту (download).
// Вызывается из socks5.go handleConn. Данные персистентны — выживают рестарт.
func (n *Node) addProxiedBytes(up, down int64) {
	if (up > 0 || down > 0) && n.traffic != nil {
		n.traffic.add(up, down)
	}
}

// runTrafficSaver периодически сохраняет счётчики трафика на диск.
// Запускается горутиной в Start() если TrafficFile настроен.
// При остановке узла (ctx.Done) выполняет финальное сохранение.
func (n *Node) runTrafficSaver() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-n.ctx.Done():
			n.traffic.save() // финальное сохранение при остановке
			return
		case <-ticker.C:
			n.traffic.saveIfDirty()
		}
	}
}

// ─── Internal ──────────────────────────────────────────────────────────────

// startListener запускает QUIC listener.
func (n *Node) startListener() error {
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return err
	}
	qcfg := &quic.Config{
		MaxIdleTimeout:       120 * time.Second, // keepalive пингует каждые 25с — 120с запас
		HandshakeIdleTimeout: 30 * time.Second,  // спутник: 3 RTT × 1900мс = 5.7с → нужен запас
		KeepAlivePeriod:      10 * time.Second,
		MaxIncomingStreams:    1000,
		MaxIncomingUniStreams: -1,
	}

	ln, err := quic.ListenAddr(n.cfg.ListenAddr, tlsCfg, qcfg)
	if err != nil {
		return fmt.Errorf("quic listen %s: %w", n.cfg.ListenAddr, err)
	}
	n.listener = ln
	log.Printf("[node] Слушаем QUIC на %s", n.cfg.ListenAddr)
	return nil
}

// acceptLoop принимает входящие QUIC соединения.
func (n *Node) acceptLoop() {
	for {
		conn, err := n.listener.Accept(n.ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Printf("[node] Accept error: %v", err)
			continue
		}

		go n.handleIncoming(conn)
	}
}

// handleIncoming обрабатывает входящее QUIC соединение.
func (n *Node) handleIncoming(conn quic.Connection) {
	remote := conn.RemoteAddr().String()
	log.Printf("[node] Входящее соединение: %s", remote)

	// Выполняем рукопожатие
	peer, err := performServerHandshake(n.ctx, conn, n.identity)
	if err != nil {
		log.Printf("[node] Handshake от %s провалился: %v", remote, err)
		conn.CloseWithError(1, "handshake failed")
		return
	}

	n.addPeer(peer)
	log.Printf("[node] ✓ Новый пир: %s (%s)", peer.NodeIDShort(), remote)

	// Обрабатываем потоки от этого пира
	peer.handleStreams(n.ctx)

	n.removePeer(peer)
	log.Printf("[node] Пир отключился: %s", peer.NodeIDShort())
}

// connectToBootstrap подключается ко всем bootstrap/relay адресам.
func (n *Node) connectToBootstrap() {
	// Собираем все адреса для подключения
	addrs := make([]string, 0, 1+len(n.cfg.BootstrapAddrs))
	if n.cfg.BootstrapAddr != "" {
		addrs = append(addrs, n.cfg.BootstrapAddr)
	}
	addrs = append(addrs, n.cfg.BootstrapAddrs...)

	if len(addrs) == 0 {
		log.Printf("[node] нет bootstrap адресов, клиент изолирован")
		return
	}

	// Запускаем горутину на каждый адрес
	for _, addr := range addrs {
		go n.connectToAddr(addr)
	}
}

// connectToBootstrapOrCache — умный старт: bootstrap → peerCache → hardcoded fallback.
// Если в конфиге нет адресов → пробуем кэш известных пиров → и только потом hardcoded.
func (n *Node) connectToBootstrapOrCache() {
	// Собираем адреса из конфига
	addrs := make([]string, 0, 1+len(n.cfg.BootstrapAddrs))
	if n.cfg.BootstrapAddr != "" {
		addrs = append(addrs, n.cfg.BootstrapAddr)
	}
	addrs = append(addrs, n.cfg.BootstrapAddrs...)

	// Если конфиг не пустой — используем его (стандартный путь)
	if len(addrs) > 0 {
		for _, addr := range addrs {
			go n.connectToAddr(addr)
		}
		return
	}

	// Нет конфига → пробуем peer cache
	cached := n.peerCache.KnownAddrs()
	if len(cached) > 0 {
		log.Printf("[dht] нет bootstrap в конфиге, используем %d пиров из кэша", len(cached))
		for _, addr := range cached {
			go n.connectToAddr(addr)
		}
		return
	}

	// Последний рубеж: hardcoded fallback адреса
	log.Printf("[dht] кэш пуст, используем hardcoded bootstrap адреса")
	for _, addr := range fallbackBootstrapAddrs {
		go n.connectToAddr(addr)
	}
}

// connectToAddr — постоянное соединение с одним адресом (переподключение).
func (n *Node) connectToAddr(addr string) {
	for {
		select {
		case <-n.ctx.Done():
			return
		default:
		}

		log.Printf("[node] Подключение к %s", addr)
		if err := n.dialPeer(addr); err != nil {
			log.Printf("[node] %s недоступен: %v. Повтор через 30с", addr, err)
			select {
			case <-n.ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}
}

// dialPeer подключается к удалённому узлу.
func (n *Node) dialPeer(addr string) error {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true, // TODO: проверка по NodeID
		NextProtos:         []string{"swarm-v1"},
	}
	qcfg := &quic.Config{
		MaxIdleTimeout:       120 * time.Second,
		HandshakeIdleTimeout: 30 * time.Second, // спутник: рукопожатие может занять 5-6с
		KeepAlivePeriod:      10 * time.Second,
	}

	conn, err := quic.DialAddr(n.ctx, addr, tlsCfg, qcfg)
	if err != nil {
		return fmt.Errorf("quic dial %s: %w", addr, err)
	}

	peer, err := performClientHandshake(n.ctx, conn, n.identity)
	if err != nil {
		conn.CloseWithError(1, "handshake failed")
		return fmt.Errorf("handshake: %w", err)
	}

	// Проверяем NodeID если задан в конфиге (защита от подмены bootstrap).
	// BootstrapNodeID — hex Ed25519 pubkey ожидаемого узла.
	if n.cfg.BootstrapNodeID != "" {
		expected, decErr := hex.DecodeString(n.cfg.BootstrapNodeID)
		if decErr == nil {
			actual := peer.nodeID[:]
			match := len(expected) == len(actual)
			if match {
				for i := range expected {
					if expected[i] != actual[i] {
						match = false
						break
					}
				}
			}
			if !match {
				conn.CloseWithError(1, "node ID mismatch")
				return fmt.Errorf("bootstrap NodeID mismatch: got %s, want %s",
					peer.NodeIDShort(), n.cfg.BootstrapNodeID[:16])
			}
		}
	}

	n.addPeer(peer)
	log.Printf("[node] ✓ Подключён к %s (%s)", peer.NodeIDShort(), addr)

	// Блокируемся пока пир активен
	peer.handleStreams(n.ctx)

	n.removePeer(peer)
	return fmt.Errorf("peer %s disconnected", peer.NodeIDShort())
}

func (n *Node) addPeer(p *Peer) {
	p.node = n // обратная ссылка для обработки MsgPeers
	n.mu.Lock()
	n.peers[p.nodeID] = p
	n.mu.Unlock()

	// Keepalive: отправляем MsgPing каждые 25с + измеряем RTT
	go p.runKeepalive(n.ctx)

	// Сохраняем пира в кэш (для DHT — рой выживает без VDS)
	remoteAddr := p.conn.RemoteAddr().String()
	go n.peerCache.Add(remoteAddr, hex.EncodeToString(p.nodeID[:]))

	// Геолокация: определяем страну и тип IP асинхронно
	go func() {
		remoteIP := extractIP(remoteAddr)
		country, ipType := lookupGeo(remoteIP)
		p.Country = country
		p.IPType = ipType
		if country != "" {
			log.Printf("[geo] пир %s: %s (%s)", p.NodeIDShort(), country, ipType)
		}
	}()

	// Bootstrap/relay анонсирует список пиров новому узлу
	if n.cfg.Mode == "bootstrap" || n.cfg.Mode == "relay" {
		go n.announceToPeer(p)
	}

	// Client: сообщаем bootstrap о готовности к обратному туннелю
	if n.cfg.Mode == "client" && p.isOutgoing {
		go n.startRelayReady(n.ctx, p)
	}
}

// extractIP извлекает IP из "host:port" строки.
func extractIP(addr string) string {
	// IPv6: "[::1]:port" → "::1"
	if strings.HasPrefix(addr, "[") {
		end := strings.LastIndex(addr, "]")
		if end > 0 {
			return addr[1:end]
		}
	}
	// IPv4/hostname: "1.2.3.4:port" → "1.2.3.4"
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

func (n *Node) removePeer(p *Peer) {
	n.mu.Lock()
	delete(n.peers, p.nodeID)
	n.mu.Unlock()
}

// ─── SOCKS5 ───────────────────────────────────────────────────────────────

// startSocks5 запускает встроенный SOCKS5 прокси.
// Трафик от браузеров/tproxy → SOCKS5 → swarm-node → рой.
func (n *Node) startSocks5() {
	s := &socks5Server{node: n}
	log.Printf("[node] SOCKS5 прокси на %s", n.cfg.Socks5Addr)
	if err := s.Listen(n.ctx, n.cfg.Socks5Addr); err != nil {
		log.Printf("[node] SOCKS5 ошибка: %v", err)
	}
}

// ─── Identity persistence ─────────────────────────────────────────────────

type savedIdentity struct {
	PrivKey []byte `json:"priv_key"`
}

func loadOrGenerateIdentity(path string) (*swarmproto.NodeIdentity, error) {
	// Пробуем загрузить существующую
	if data, err := os.ReadFile(path); err == nil {
		var saved savedIdentity
		if err := json.Unmarshal(data, &saved); err == nil && len(saved.PrivKey) == ed25519.PrivateKeySize {
			priv := ed25519.PrivateKey(saved.PrivKey)
			return &swarmproto.NodeIdentity{
				PrivKey: priv,
				PubKey:  priv.Public().(ed25519.PublicKey),
			}, nil
		}
	}

	// Генерируем новую
	id, err := swarmproto.GenerateIdentity()
	if err != nil {
		return nil, err
	}

	// Сохраняем
	saved := savedIdentity{PrivKey: []byte(id.PrivKey)}
	data, _ := json.MarshalIndent(saved, "", "  ")
	if err := os.WriteFile(path, data, 0600); err != nil {
		log.Printf("[node] предупреждение: не удалось сохранить identity: %v", err)
	}

	log.Printf("[node] Новая идентичность создана: %s", hex.EncodeToString(id.PubKey)[:16])
	return id, nil
}

// ─── TLS для QUIC ─────────────────────────────────────────────────────────

func generateTLSConfig() (*tls.Config, error) {
	// Самоподписанный сертификат для QUIC handshake.
	// Реальная аутентификация — через наш Ed25519+X25519 протокол.
	key, err := generateTLSKey()
	if err != nil {
		return nil, fmt.Errorf("tls keygen: %w", err)
	}
	cert, err := generateSelfSigned(key)
	if err != nil {
		return nil, fmt.Errorf("tls cert: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"swarm-v1"},
	}, nil
}

func generateTLSKey() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return priv, err
}

func generateSelfSigned(key ed25519.PrivateKey) (tls.Certificate, error) {
	// Самоподписанный x509 сертификат для QUIC handshake.
	// Реальная аутентификация узлов выполняется нашим протоколом (Ed25519 + X25519),
	// поэтому TLS используется только как транспорт — InsecureSkipVerify на клиенте.
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "swarm-node"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour), // 10 лет
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	pub := key.Public()
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("x509.CreateCertificate: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}
