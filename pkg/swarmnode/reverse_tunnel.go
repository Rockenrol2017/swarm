package swarmnode

// reverse_tunnel.go — обратный туннель: каждый клиент = узел роя.
//
// Принцип:
//   После подключения к bootstrap клиент отправляет MsgRelayReady.
//   Bootstrap регистрирует клиента как potential relay.
//   Когда bootstrap хочет форвардировать чужой трафик через клиента,
//   он отправляет MsgRelayRequest, клиент отвечает Accept/Reject.
//
// Автолимит нагрузки:
//   Если загрузка канала > 80% → MsgRelayReject (клиент перегружен).
//   Иначе → MsgRelayAccept.
//   MaxRelayPercent = 20% для client, 100% для bootstrap.
//
// Минимум: 5% от канала всегда доступно для роя (нельзя отключить).
// Keepalive: клиент повторяет MsgRelayReady каждые 30с.

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const (
	relayKeepaliveInterval = 30 * time.Second
	minRelayPercent        = 5 // минимальный % для relay (нельзя отключить)
)

// reversePeer — зарегистрированный обратный туннель (client готов к relay).
type reversePeer struct {
	peer       *Peer
	maxPercent uint8
	registered time.Time
}

// reverseRegistry — реестр пиров готовых к relay на bootstrap узле.
type reverseRegistry struct {
	mu    sync.RWMutex
	peers map[[32]byte]*reversePeer // nodeID → reversePeer
}

// relayCount — текущее количество активных relay соединений через нас.
var relayCount atomic.Int64

// shouldAcceptRelay проверяет, можем ли мы принять relay запрос.
// Используется клиентом при получении MsgRelayRequest.
func (n *Node) shouldAcceptRelay() bool {
	maxPct := n.cfg.MaxRelayPercent
	if maxPct <= 0 {
		// Дефолты: client=20%, bootstrap/relay=100%
		if n.cfg.Mode == "client" {
			maxPct = 20
		} else {
			maxPct = 100
		}
	}
	// Минимум 5% всегда
	if maxPct < minRelayPercent {
		maxPct = minRelayPercent
	}

	// Проверяем загрузку канала
	load := n.bw.CurrentLoadPercent()
	if load > 80 {
		return false // канал перегружен
	}

	// Проверяем текущее количество relay соединений
	current := relayCount.Load()
	limit := int64(maxPct) // упрощённое ограничение по количеству
	return current < limit
}

// startRelayReady запускает keepalive для обратного туннеля.
// Вызывается при подключении клиента к bootstrap.
// Периодически отправляет MsgRelayReady, сообщая bootstrap о готовности.
func (n *Node) startRelayReady(ctx context.Context, p *Peer) {
	if n.cfg.Mode != "client" {
		return // только client узлы участвуют в обратном туннеле
	}

	// Отправляем немедленно при подключении
	n.sendRelayReady(p)

	ticker := time.NewTicker(relayKeepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if p.IsClosed() {
				return
			}
			n.sendRelayReady(p)
		}
	}
}

// sendRelayReady отправляет MsgRelayReady на bootstrap.
// payload: [1 byte] MaxRelayPercent
func (n *Node) sendRelayReady(p *Peer) {
	pct := n.cfg.MaxRelayPercent
	if pct <= 0 {
		pct = 20 // дефолт для client
	}
	if pct < minRelayPercent {
		pct = minRelayPercent
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	stream, err := p.conn.OpenStreamSync(ctx)
	if err != nil {
		return
	}
	defer stream.Close()

	if err := writeMsg(stream, p.sendCipher, MsgRelayReady, []byte{byte(pct)}); err != nil {
		log.Printf("[relay] ошибка отправки MsgRelayReady: %v", err)
	}
}

// handleRelayReady обрабатывает MsgRelayReady от клиента (на bootstrap).
// Регистрирует клиента как потенциальный relay.
func (n *Node) handleRelayReady(p *Peer, payload []byte) {
	var maxPct uint8 = 20
	if len(payload) > 0 {
		maxPct = payload[0]
	}

	log.Printf("[relay] пир %s готов к relay (max %d%%)", p.NodeIDShort(), maxPct)

	// TODO: в будущем bootstrap будет использовать зарегистрированных
	// клиентов для форвардирования трафика через них.
	// Сейчас только логируем.
}

// handleRelayRequest обрабатывает MsgRelayRequest от bootstrap (на client).
// Если клиент свободен — отвечает MsgRelayAccept, иначе MsgRelayReject.
func (n *Node) handleRelayRequest(p *Peer, payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := p.conn.OpenStreamSync(ctx)
	if err != nil {
		return
	}
	defer stream.Close()

	if n.shouldAcceptRelay() {
		relayCount.Add(1)
		defer relayCount.Add(-1)
		writeMsg(stream, p.sendCipher, MsgRelayAccept, nil)
		log.Printf("[relay] принят relay запрос от %s", p.NodeIDShort())
	} else {
		writeMsg(stream, p.sendCipher, MsgRelayReject, nil)
		log.Printf("[relay] отклонён relay запрос от %s (перегружен)", p.NodeIDShort())
	}
}
