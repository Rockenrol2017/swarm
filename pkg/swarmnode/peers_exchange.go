package swarmnode

// peers_exchange.go — обмен списком пиров (MsgPeers).
//
// Протокол:
//   1. При подключении нового пира → bootstrap/relay отправляет ему список известных узлов
//   2. Клиент получает список → может подключиться к другим узлам напрямую
//   3. Каждые 60 секунд → keepalive ping + обновление списка
//
// Формат MsgPeers payload:
//   [2 bytes] count
//   Для каждого пира:
//     [32 bytes] NodeID
//     [1 byte]   len(addr)
//     [N bytes]  addr ("host:port")

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"time"
)

const (
	peerExchangeInterval = 60 * time.Second
	maxPeersInExchange   = 20 // максимум пиров в одном MsgPeers
)

// PeerInfo — краткая информация о пире для обмена.
type PeerInfo struct {
	NodeID [32]byte
	Addr   string // "host:port" для QUIC подключения
}

// sendPeerList отправляет текущий список пиров другому узлу.
func (p *Peer) sendPeerList(peers []*PeerInfo) error {
	if len(peers) == 0 {
		return nil
	}
	if len(peers) > maxPeersInExchange {
		peers = peers[:maxPeersInExchange]
	}

	// Сериализуем
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf[:2], uint16(len(peers)))

	for _, pi := range peers {
		addrBytes := []byte(pi.Addr)
		entry := make([]byte, 32+1+len(addrBytes))
		copy(entry[:32], pi.NodeID[:])
		entry[32] = byte(len(addrBytes))
		copy(entry[33:], addrBytes)
		buf = append(buf, entry...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stream, err := p.conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open peer list stream: %w", err)
	}
	defer stream.Close()

	return writeMsg(stream, p.sendCipher, MsgPeers, buf)
}

// parsePeerList десериализует MsgPeers payload.
func parsePeerList(payload []byte) ([]*PeerInfo, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("peer list too short")
	}

	count := int(binary.BigEndian.Uint16(payload[:2]))
	payload = payload[2:]

	peers := make([]*PeerInfo, 0, count)
	for i := 0; i < count; i++ {
		if len(payload) < 33 {
			break
		}
		pi := &PeerInfo{}
		copy(pi.NodeID[:], payload[:32])
		addrLen := int(payload[32])
		payload = payload[33:]
		if len(payload) < addrLen {
			break
		}
		pi.Addr = string(payload[:addrLen])
		payload = payload[addrLen:]
		peers = append(peers, pi)
	}

	return peers, nil
}

// announceToPeer отправляет список пиров новому узлу (вызывается bootstrap/relay).
func (n *Node) announceToPeer(newPeer *Peer) {
	n.mu.RLock()
	var infos []*PeerInfo
	for _, p := range n.peers {
		if p == newPeer {
			continue // не отправляем пиру его самого
		}
		addr := p.conn.RemoteAddr().String()
		infos = append(infos, &PeerInfo{
			NodeID: p.nodeID,
			Addr:   addr,
		})
		if len(infos) >= maxPeersInExchange {
			break
		}
	}
	n.mu.RUnlock()

	if len(infos) == 0 {
		return
	}

	if err := newPeer.sendPeerList(infos); err != nil {
		log.Printf("[peers] ошибка отправки списка пиров %s: %v", newPeer.NodeIDShort(), err)
		return
	}
	log.Printf("[peers] отправлено %d пиров → %s", len(infos), newPeer.NodeIDShort())
}

// handlePeerList обрабатывает входящий список пиров.
// Пытается подключиться к новым узлам которых ещё нет.
func (n *Node) handlePeerList(from *Peer, payload []byte) {
	peers, err := parsePeerList(payload)
	if err != nil {
		log.Printf("[peers] ошибка парсинга peer list от %s: %v", from.NodeIDShort(), err)
		return
	}

	log.Printf("[peers] получено %d пиров от %s", len(peers), from.NodeIDShort())

	for _, pi := range peers {
		// Уже знаем этого пира?
		n.mu.RLock()
		_, known := n.peers[pi.NodeID]
		n.mu.RUnlock()
		if known {
			continue
		}

		// Это мы сами?
		if pi.NodeID == n.identity.NodeID() {
			continue
		}

		// Подключаемся в фоне
		go func(addr string) {
			log.Printf("[peers] подключаемся к новому пиру: %s", addr)
			if err := n.dialPeer(addr); err != nil {
				log.Printf("[peers] не удалось подключиться к %s: %v", addr, err)
			}
		}(pi.Addr)
	}
}

// startPeerExchange запускает периодический обмен пирами.
func (n *Node) startPeerExchange() {
	ticker := time.NewTicker(peerExchangeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.exchangePeersWithAll()
		}
	}
}

// exchangePeersWithAll отправляет актуальный список пиров всем подключённым узлам.
func (n *Node) exchangePeersWithAll() {
	n.mu.RLock()
	peers := make([]*Peer, 0, len(n.peers))
	for _, p := range n.peers {
		peers = append(peers, p)
	}
	n.mu.RUnlock()

	if len(peers) < 2 {
		return // не с кем обмениваться
	}

	for _, p := range peers {
		go n.announceToPeer(p)
	}
}
