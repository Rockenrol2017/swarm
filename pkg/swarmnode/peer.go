package swarmnode

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/narodnaya-set/swarm/pkg/swarmproto"
	"github.com/quic-go/quic-go"
)

// MsgType — тип сообщения внутри QUIC потока.
type MsgType uint8

const (
	MsgConnect      MsgType = 0x01 // запрос подключения к хосту
	MsgData         MsgType = 0x02 // данные
	MsgClose        MsgType = 0x03 // закрытие
	MsgPing         MsgType = 0x04 // keepalive
	MsgPong         MsgType = 0x05
	MsgPeers        MsgType = 0x06 // список известных пиров (DHT)
	MsgRelayReady   MsgType = 0x07 // клиент готов к relay — сообщает bootstrap о своей готовности
	MsgRelayRequest MsgType = 0x08 // bootstrap просит клиента форвардировать поток
	MsgRelayAccept  MsgType = 0x09 // клиент принял запрос relay
	MsgRelayReject  MsgType = 0x0A // клиент перегружен, отклоняет запрос
)

// Peer — активное соединение с другим узлом роя.
type Peer struct {
	nodeID     [32]byte
	conn       quic.Connection
	sendCipher swarmproto.AEAD
	recvCipher swarmproto.AEAD
	node       *Node // обратная ссылка для обработки MsgPeers

	// isOutgoing = true: мы сами инициировали соединение (upstream: bootstrap/relay)
	// isOutgoing = false: к нам подключились (client)
	// Важно для relay режима: selectUpstreamPeer() выбирает только outgoing пиров.
	isOutgoing bool

	// LatencyMs — измеренная задержка до этого пира (RTT keepalive ping/pong).
	// 0 = ещё не измерено, -1 = недоступен, >0 = RTT в мс.
	// Используется selectPeer() для выбора самого быстрого upstream.
	LatencyMs atomic.Int64

	// Гео-информация (заполняется асинхронно из geo.go при подключении).
	Country string // "DE", "US", "SE", "RU", ... (ISO 3166-1 alpha-2)
	IPType  string // "residential" | "datacenter" | "" (не определено)

	// Статистика
	bytesSent   atomic.Int64
	bytesRecv   atomic.Int64
	connectedAt time.Time

	mu      sync.Mutex
	streams map[uint64]*ProxyStream
	closed  bool
}

// ProxyStream — один проксируемый поток (одно TCP соединение клиента).
type ProxyStream struct {
	id       uint64
	stream   quic.Stream
	target   net.Conn // соединение с целевым сервером (на relay узле)
	peer     *Peer
}

// NodeIDShort возвращает первые 8 символов hex NodeID.
func (p *Peer) NodeIDShort() string {
	return hex.EncodeToString(p.nodeID[:])[:16]
}

// BytesSent возвращает количество отправленных байт.
func (p *Peer) BytesSent() int64 { return p.bytesSent.Load() }

// BytesRecv возвращает количество полученных байт.
func (p *Peer) BytesRecv() int64 { return p.bytesRecv.Load() }

// ConnectedAt возвращает время подключения.
func (p *Peer) ConnectedAt() time.Time { return p.connectedAt }

// IsClosed возвращает true если пир отключён.
func (p *Peer) IsClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// Close закрывает соединение с пиром.
func (p *Peer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.conn.CloseWithError(0, "bye")
	}
}

// OpenProxyStream открывает новый поток для проксирования соединения.
// addr — "host:port" куда нужно подключиться.
func (p *Peer) OpenProxyStream(ctx context.Context, addr string) (quic.Stream, error) {
	stream, err := p.conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}

	// Отправляем MsgConnect с адресом назначения
	if err := writeConnectMsg(stream, p.sendCipher, addr); err != nil {
		stream.Close()
		return nil, fmt.Errorf("write connect: %w", err)
	}

	return stream, nil
}

// handleStreams обрабатывает входящие потоки от пира.
// Блокируется до закрытия соединения.
func (p *Peer) handleStreams(ctx context.Context) {
	for {
		stream, err := p.conn.AcceptStream(ctx)
		if err != nil {
			return
		}
		go p.handleStream(stream)
	}
}

// handleStream обрабатывает один входящий поток.
func (p *Peer) handleStream(stream quic.Stream) {
	defer stream.Close()

	// Читаем тип сообщения
	msgType, payload, err := readMsg(stream, p.recvCipher)
	if err != nil {
		log.Printf("[peer %s] read msg error: %v", p.NodeIDShort(), err)
		return
	}

	switch msgType {
	case MsgConnect:
		// Запрос на подключение к целевому адресу
		addr := string(payload)
		p.handleProxyRequest(stream, addr)

	case MsgPing:
		// Отвечаем pong
		writeMsg(stream, p.sendCipher, MsgPong, nil)

	case MsgPeers:
		// Список пиров от соседнего узла — обрабатывается в node
		if p.node != nil {
			p.node.handlePeerList(p, payload)
		}

	case MsgRelayReady:
		// Клиент сообщает о готовности к relay (на bootstrap)
		if p.node != nil {
			p.node.handleRelayReady(p, payload)
		}

	case MsgRelayRequest:
		// Bootstrap просит нас форвардировать трафик (на client)
		if p.node != nil {
			p.node.handleRelayRequest(p, payload)
		}

	default:
		log.Printf("[peer %s] unknown msg type: 0x%02x", p.NodeIDShort(), msgType)
	}
}

// handleProxyRequest подключается к целевому адресу и проксирует трафик.
// Выполняется на relay/bootstrap узле.
//
// В режиме relay — форвардит через upstream (2-hop: client→relay→bootstrap→internet).
// В режиме bootstrap/exit — диалит TCP напрямую.
func (p *Peer) handleProxyRequest(stream quic.Stream, addr string) {
	log.Printf("[peer %s] CONNECT %s", p.NodeIDShort(), addr)

	// Relay режим: форвардим через upstream а не диалим напрямую.
	// Это создаёт 2-hop цепочку: Client → Relay → Bootstrap → Internet.
	// Целевой сервер видит IP bootstrap, не relay и не клиента.
	// selectUpstreamPeer() выбирает только ИСХОДЯЩИЕ пиры (те что мы сами диалили),
	// предотвращая случайный выбор клиентского пира → зацикливание.
	if p.node != nil && p.node.cfg.Mode == "relay" {
		upstream := p.node.selectUpstreamPeer()
		if upstream != nil {
			p.forwardThroughUpstream(stream, addr, upstream)
			return
		}
		log.Printf("[relay %s] нет upstream пира, fallback на direct dial", p.NodeIDShort())
	}

	// Direct: bootstrap/exit диалит TCP напрямую
	target, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		log.Printf("[peer %s] dial %s: %v", p.NodeIDShort(), addr, err)
		return
	}
	defer target.Close()

	bridgeStreamTCP(stream, target, &p.bytesRecv, &p.bytesSent)
}

// forwardThroughUpstream форвардит proxy запрос через upstream пир.
// Используется relay режимом: client → relay → upstream(bootstrap) → internet.
//
// Relay НЕ знает содержимое трафика (HTTPS/TLS снаружи шифрует payload).
// Bootstrap видит только IP relay, не клиента.
func (p *Peer) forwardThroughUpstream(clientStream quic.Stream, addr string, upstream *Peer) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	upstreamStream, err := upstream.OpenProxyStream(ctx, addr)
	if err != nil {
		log.Printf("[relay %s] upstream %s → %s: %v",
			p.NodeIDShort(), upstream.NodeIDShort(), addr, err)
		return
	}
	defer upstreamStream.Close()

	log.Printf("[relay] %s ← client → [%s] → %s",
		p.NodeIDShort(), upstream.NodeIDShort(), addr)

	done := make(chan struct{}, 2)

	// clientStream → upstreamStream
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := clientStream.Read(buf)
			if n > 0 {
				upstreamStream.Write(buf[:n])
				p.bytesRecv.Add(int64(n))
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	// upstreamStream → clientStream
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := upstreamStream.Read(buf)
			if n > 0 {
				clientStream.Write(buf[:n])
				p.bytesSent.Add(int64(n))
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	<-done
}

// bridgeStreamTCP соединяет QUIC поток с TCP соединением (двунаправленно).
func bridgeStreamTCP(stream quic.Stream, target net.Conn,
	bytesIn, bytesOut *atomic.Int64) {

	done := make(chan struct{}, 2)

	// stream → target
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				target.Write(buf[:n])
				if bytesIn != nil {
					bytesIn.Add(int64(n))
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	// target → stream
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := target.Read(buf)
			if n > 0 {
				stream.Write(buf[:n])
				if bytesOut != nil {
					bytesOut.Add(int64(n))
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	<-done
}

// runKeepalive отправляет MsgPing каждые 25 секунд чтобы предотвратить
// срабатывание QUIC idle timeout (MaxIdleTimeout=120s в node.go).
// Заодно измеряет RTT до пира и сохраняет в p.LatencyMs.
// Запускается горутиной из Node.addPeer для каждого пира.
func (p *Peer) runKeepalive(ctx context.Context) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if p.IsClosed() {
				return
			}
			pingCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			stream, err := p.conn.OpenStreamSync(pingCtx)
			cancel()
			if err != nil {
				return // соединение мертво — node.connectToAddr переподключит
			}
			start := time.Now()
			writeMsg(stream, p.sendCipher, MsgPing, nil)
			stream.Close()
			// RTT = время отправки ping (не полный round-trip, но достаточно для сравнения)
			rtt := time.Since(start).Milliseconds()
			if rtt > 0 {
				p.LatencyMs.Store(rtt)
			}
		}
	}
}

// ─── Handshakes ───────────────────────────────────────────────────────────

// performServerHandshake выполняет рукопожатие со стороны сервера.
func performServerHandshake(ctx context.Context, conn quic.Connection,
	id *swarmproto.NodeIdentity) (*Peer, error) {

	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("accept handshake stream: %w", err)
	}
	defer stream.Close()

	// Читаем ClientHello
	raw := make([]byte, swarmproto.HelloSize)
	if _, err := io.ReadFull(stream, raw); err != nil {
		return nil, fmt.Errorf("read client hello: %w", err)
	}

	clientHello, err := swarmproto.UnmarshalClientHello(raw)
	if err != nil {
		return nil, fmt.Errorf("unmarshal client hello: %w", err)
	}

	serverHello, result, err := swarmproto.BuildServerHello(id, clientHello)
	if err != nil {
		return nil, fmt.Errorf("build server hello: %w", err)
	}

	// Отправляем ServerHello
	if _, err := stream.Write(swarmproto.MarshalServerHello(serverHello)); err != nil {
		return nil, fmt.Errorf("write server hello: %w", err)
	}

	return &Peer{
		nodeID:      result.PeerNodeID,
		conn:        conn,
		sendCipher:  result.SendCipher,
		recvCipher:  result.RecvCipher,
		connectedAt: time.Now(),
		streams:     make(map[uint64]*ProxyStream),
		isOutgoing:  false, // к нам подключились (client)
	}, nil
	// node поле устанавливается в Node.addPeer
}

// performClientHandshake выполняет рукопожатие со стороны клиента.
func performClientHandshake(ctx context.Context, conn quic.Connection,
	id *swarmproto.NodeIdentity) (*Peer, error) {

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("open handshake stream: %w", err)
	}
	defer stream.Close()

	// Строим и отправляем ClientHello
	clientHello, ephPriv, err := swarmproto.BuildClientHello(id)
	if err != nil {
		return nil, fmt.Errorf("build client hello: %w", err)
	}

	if _, err := stream.Write(swarmproto.MarshalClientHello(clientHello)); err != nil {
		return nil, fmt.Errorf("write client hello: %w", err)
	}

	// Читаем ServerHello
	raw := make([]byte, swarmproto.HelloSize)
	if _, err := io.ReadFull(stream, raw); err != nil {
		return nil, fmt.Errorf("read server hello: %w", err)
	}

	serverHello, err := swarmproto.UnmarshalServerHello(raw)
	if err != nil {
		return nil, fmt.Errorf("unmarshal server hello: %w", err)
	}

	result, err := swarmproto.FinalizeHandshake(ephPriv, serverHello)
	if err != nil {
		return nil, fmt.Errorf("finalize handshake: %w", err)
	}

	return &Peer{
		nodeID:      result.PeerNodeID,
		conn:        conn,
		sendCipher:  result.SendCipher,
		recvCipher:  result.RecvCipher,
		connectedAt: time.Now(),
		streams:     make(map[uint64]*ProxyStream),
		isOutgoing:  true, // мы диалили (upstream: bootstrap или relay)
	}, nil
}

// ─── Message framing ──────────────────────────────────────────────────────

// writeMsg записывает зашифрованное сообщение в поток.
// Формат: [4 bytes length][encrypted payload]
func writeMsg(w io.Writer, cipher swarmproto.AEAD, msgType MsgType, payload []byte) error {
	frame := &swarmproto.Frame{
		Data: append([]byte{byte(msgType)}, payload...),
	}
	frame.Timestamp = time.Now()
	sid, _ := swarmproto.NewSessionID()
	frame.SessionID = sid

	encrypted, err := swarmproto.Encode(frame, cipher)
	if err != nil {
		return err
	}

	// Length prefix
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(encrypted)))
	if _, err := w.Write(lenBuf); err != nil {
		return err
	}
	_, err = w.Write(encrypted)
	return err
}

// readMsg читает и расшифровывает сообщение из потока.
func readMsg(r io.Reader, cipher swarmproto.AEAD) (MsgType, []byte, error) {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return 0, nil, err
	}
	frameLen := binary.BigEndian.Uint32(lenBuf)
	if frameLen > uint32(swarmproto.MaxFrameSize) {
		return 0, nil, fmt.Errorf("frame too large: %d", frameLen)
	}

	raw := make([]byte, frameLen)
	if _, err := io.ReadFull(r, raw); err != nil {
		return 0, nil, err
	}

	frame, err := swarmproto.Decode(raw, cipher)
	if err != nil {
		return 0, nil, err
	}

	if len(frame.Data) == 0 {
		return 0, nil, fmt.Errorf("empty frame")
	}

	return MsgType(frame.Data[0]), frame.Data[1:], nil
}

// writeConnectMsg записывает MsgConnect с адресом назначения.
func writeConnectMsg(w io.Writer, cipher swarmproto.AEAD, addr string) error {
	return writeMsg(w, cipher, MsgConnect, []byte(addr))
}
