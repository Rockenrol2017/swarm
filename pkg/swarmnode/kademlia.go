package swarmnode

// kademlia.go — DHT routing table на основе Kademlia XOR metric.
//
// Параметры адаптированы для спутникового RTT (Concilium v0.2.2):
//   - alpha = 1  (последовательные запросы, не параллельные — satellite expensive)
//   - k     = 10 (bucket size, вместо стандартных 20)
//   - timeout = 8s (4× RTT спутника)
//   - refresh = 2h (экономим спутниковый трафик)
//
// Dual-stack режим (v0.3):
//   Bootstrap VDS + DHT работают параллельно.
//   DHT не заменяет bootstrap пока нод < 20.
//
// NodeID: 32 байта (256 бит) = полный Ed25519 pubkey.
// XOR distance между двумя NodeID определяет «близость» в keyspace.

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

const (
	// KademliaK — размер k-bucket (нод на один bucket).
	// Concilium: 10 вместо стандартных 20 — для малой сети меньше пустых слотов.
	KademliaK = 10

	// KademliaAlpha — параллельность lookup.
	// 1 = последовательно. При RTT 1800ms параллельные запросы только засоряют канал.
	KademliaAlpha = 1

	// KademliaTimeout — таймаут одного запроса FindNode.
	// 8с = 4× максимальный спутниковый RTT (1800ms × 4 = 7.2s).
	KademliaTimeout = 8 * time.Second

	// KademliaRefresh — интервал обновления routing table.
	// 2h вместо стандартных 1h — экономим спутниковый трафик.
	KademliaRefresh = 2 * time.Hour

	// IDLen — длина NodeID в байтах.
	IDLen = 32

	// IDBits — длина NodeID в битах = количество k-buckets.
	IDBits = IDLen * 8 // 256
)

// NodeID — 256-битный идентификатор ноды (Ed25519 pubkey).
type NodeID = [IDLen]byte

// xorDistance вычисляет XOR расстояние между двумя NodeID.
// Меньше = ближе в keyspace.
func xorDistance(a, b NodeID) [IDLen]byte {
	var d [IDLen]byte
	for i := range d {
		d[i] = a[i] ^ b[i]
	}
	return d
}

// bucketIndex возвращает индекс k-bucket для данного XOR расстояния.
// Bucket 0 = самый близкий (первый отличающийся бит = бит 0 слева).
// Bucket 255 = самый далёкий.
func bucketIndex(dist [IDLen]byte) int {
	for i := 0; i < IDLen; i++ {
		if dist[i] == 0 {
			continue
		}
		// Находим первый установленный бит
		b := dist[i]
		for bit := 7; bit >= 0; bit-- {
			if b&(1<<uint(bit)) != 0 {
				return i*8 + (7 - bit)
			}
		}
	}
	return IDBits - 1 // одинаковые ID (себя не добавляем)
}

// kBucket — один сегмент routing table.
// Хранит до K ближайших нод в данном диапазоне keyspace.
type kBucket struct {
	mu    sync.Mutex
	nodes []*PeerInfo // упорядочены по времени последней активности (хвост = самый свежий)
}

// add добавляет или обновляет ноду в bucket.
// Если bucket полный — новая нода отклоняется (Kademlia: старые узлы надёжнее).
func (b *kBucket) add(pi *PeerInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Ищем существующую запись
	for i, n := range b.nodes {
		if n.NodeID == pi.NodeID {
			// Перемещаем в хвост (самый свежий)
			b.nodes = append(b.nodes[:i], b.nodes[i+1:]...)
			b.nodes = append(b.nodes, pi)
			return
		}
	}

	if len(b.nodes) < KademliaK {
		b.nodes = append(b.nodes, pi)
		return
	}
	// Bucket полный — по Kademlia отклоняем (не вытесняем старые узлы).
	// В будущем можно добавить ping-and-replace логику.
}

// remove удаляет ноду из bucket (при обнаружении недоступности).
func (b *kBucket) remove(id NodeID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, n := range b.nodes {
		if n.NodeID == id {
			b.nodes = append(b.nodes[:i], b.nodes[i+1:]...)
			return
		}
	}
}

// snapshot возвращает копию текущих нод.
func (b *kBucket) snapshot() []*PeerInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*PeerInfo, len(b.nodes))
	copy(out, b.nodes)
	return out
}

// ─── Routing Table ────────────────────────────────────────────────────────────

// RoutingTable — полная таблица маршрутизации Kademlia.
// IDBits k-buckets, каждый хранит до K нод.
type RoutingTable struct {
	selfID  NodeID
	buckets [IDBits]*kBucket
}

// newRoutingTable создаёт таблицу для данного узла.
func newRoutingTable(selfID NodeID) *RoutingTable {
	rt := &RoutingTable{selfID: selfID}
	for i := range rt.buckets {
		rt.buckets[i] = &kBucket{}
	}
	return rt
}

// Add добавляет ноду в routing table.
// Ноду себя самого игнорирует.
func (rt *RoutingTable) Add(pi *PeerInfo) {
	if pi.NodeID == rt.selfID {
		return
	}
	dist := xorDistance(rt.selfID, pi.NodeID)
	idx := bucketIndex(dist)
	rt.buckets[idx].add(pi)
}

// Remove помечает ноду как недоступную.
func (rt *RoutingTable) Remove(id NodeID) {
	dist := xorDistance(rt.selfID, id)
	idx := bucketIndex(dist)
	rt.buckets[idx].remove(id)
}

// distEntry — нода с вычисленным расстоянием (для сортировки).
type distEntry struct {
	pi   *PeerInfo
	dist [IDLen]byte
}

// FindClosest возвращает до count нод ближайших к target по XOR метрике.
func (rt *RoutingTable) FindClosest(target NodeID, count int) []*PeerInfo {
	// Собираем всех кандидатов
	var candidates []distEntry
	for _, b := range rt.buckets {
		for _, pi := range b.snapshot() {
			d := xorDistance(target, pi.NodeID)
			candidates = append(candidates, distEntry{pi, d})
		}
	}

	// Сортируем по XOR расстоянию
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].dist, candidates[j].dist
		for k := 0; k < IDLen; k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})

	// Возвращаем top-count
	if len(candidates) > count {
		candidates = candidates[:count]
	}
	result := make([]*PeerInfo, len(candidates))
	for i, c := range candidates {
		result[i] = c.pi
	}
	return result
}

// Size возвращает общее количество нод в routing table.
func (rt *RoutingTable) Size() int {
	total := 0
	for _, b := range rt.buckets {
		b.mu.Lock()
		total += len(b.nodes)
		b.mu.Unlock()
	}
	return total
}

// ─── Kademlia DHT ─────────────────────────────────────────────────────────────

// KademliaDHT — слой DHT поверх существующей peer-сети.
// Dual-stack: работает параллельно с bootstrap VDS.
type KademliaDHT struct {
	node  *Node
	table *RoutingTable
	mu    sync.Mutex
}

// newKademliaDHT создаёт DHT для узла.
func newKademliaDHT(n *Node) *KademliaDHT {
	return &KademliaDHT{
		node:  n,
		table: newRoutingTable(n.identity.NodeID()),
	}
}

// AddPeer добавляет пира в routing table (вызывается при каждом успешном подключении).
func (d *KademliaDHT) AddPeer(pi *PeerInfo) {
	d.table.Add(pi)
}

// RemovePeer удаляет пира из routing table (вызывается при отключении).
func (d *KademliaDHT) RemovePeer(id NodeID) {
	d.table.Remove(id)
}

// FindClosestLocal возвращает k ближайших нод из локальной routing table.
func (d *KademliaDHT) FindClosestLocal(target NodeID) []*PeerInfo {
	return d.table.FindClosest(target, KademliaK)
}

// Lookup выполняет итеративный Kademlia lookup для target.
// Возвращает k ближайших нод которые удалось найти в сети.
// alpha=1: запросы последовательные (оптимально для спутника).
func (d *KademliaDHT) Lookup(ctx context.Context, target NodeID) ([]*PeerInfo, error) {
	// Начинаем с k ближайших из локальной таблицы
	closest := d.table.FindClosest(target, KademliaK)
	if len(closest) == 0 {
		return nil, nil
	}

	// Множество уже опрошенных нод
	queried := make(map[NodeID]bool)
	queried[d.node.identity.NodeID()] = true

	// Итеративный поиск
	for iteration := 0; iteration < 20; iteration++ { // max 20 раундов
		// Ищем первую неопрошенную ноду среди k ближайших
		var toQuery *PeerInfo
		for _, pi := range closest {
			if !queried[pi.NodeID] {
				toQuery = pi
				break
			}
		}
		if toQuery == nil {
			break // все ближайшие уже опрошены
		}

		queried[toQuery.NodeID] = true

		// Отправляем FindNode запрос (alpha=1: один за раз)
		qctx, cancel := context.WithTimeout(ctx, KademliaTimeout)
		found, err := d.sendFindNode(qctx, toQuery, target)
		cancel()

		if err != nil {
			log.Printf("[dht] FindNode %s → %s: %v",
				fmt.Sprintf("%x", toQuery.NodeID[:4]),
				fmt.Sprintf("%x", target[:4]), err)
			d.table.Remove(toQuery.NodeID)
			continue
		}

		// Добавляем найденные ноды в таблицу и в кандидаты
		for _, pi := range found {
			d.table.Add(pi)
		}

		// Пересчитываем k ближайших с новыми данными
		newClosest := d.table.FindClosest(target, KademliaK)

		// Проверяем сходимость: если список не изменился — готово
		if sameClosest(closest, newClosest) {
			break
		}
		closest = newClosest
	}

	return closest, nil
}

// Bootstrap выполняет начальный lookup для собственного NodeID.
// Заполняет routing table ближайшими нодами.
// Вызывается после подключения к первым пирам.
func (d *KademliaDHT) Bootstrap(ctx context.Context) {
	selfID := d.node.identity.NodeID()
	found, err := d.Lookup(ctx, selfID)
	if err != nil {
		log.Printf("[dht] bootstrap lookup failed: %v", err)
		return
	}
	log.Printf("[dht] bootstrap: routing table заполнена, %d нод найдено (routing size: %d)",
		len(found), d.table.Size())
}

// startRefresh запускает периодическое обновление routing table.
func (d *KademliaDHT) startRefresh(ctx context.Context) {
	ticker := time.NewTicker(KademliaRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			log.Printf("[dht] refresh routing table (size: %d)", d.table.Size())
			refreshCtx, cancel := context.WithTimeout(ctx, KademliaTimeout*10)
			d.Bootstrap(refreshCtx)
			cancel()
		}
	}
}

// ─── FindNode protocol helpers ────────────────────────────────────────────────

// sendFindNode отправляет MsgFindNode запрос пиру и ждёт MsgFindNodeResp.
func (d *KademliaDHT) sendFindNode(ctx context.Context, to *PeerInfo, target NodeID) ([]*PeerInfo, error) {
	// Находим активное соединение с этим пиром
	d.node.mu.RLock()
	peer := d.node.peers[to.NodeID]
	d.node.mu.RUnlock()

	if peer == nil {
		return nil, fmt.Errorf("пир не подключён")
	}

	return peer.findNode(ctx, target)
}

// sameClosest проверяет совпадение двух списков ближайших нод.
func sameClosest(a, b []*PeerInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].NodeID != b[i].NodeID {
			return false
		}
	}
	return true
}

// ─── Wire format для FindNode / FindNodeResp ──────────────────────────────────

// marshalFindNode сериализует запрос: [32 bytes target NodeID]
func marshalFindNode(target NodeID) []byte {
	payload := make([]byte, IDLen)
	copy(payload, target[:])
	return payload
}

// unmarshalFindNode десериализует запрос.
func unmarshalFindNode(payload []byte) (NodeID, error) {
	if len(payload) < IDLen {
		return NodeID{}, fmt.Errorf("FindNode payload too short: %d", len(payload))
	}
	var id NodeID
	copy(id[:], payload[:IDLen])
	return id, nil
}

// marshalFindNodeResp сериализует ответ в формате MsgPeers (reuse).
func marshalFindNodeResp(peers []*PeerInfo) []byte {
	if len(peers) > KademliaK {
		peers = peers[:KademliaK]
	}
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, uint16(len(peers)))
	for _, pi := range peers {
		addrB := []byte(pi.Addr)
		entry := make([]byte, IDLen+1+len(addrB))
		copy(entry[:IDLen], pi.NodeID[:])
		entry[IDLen] = byte(len(addrB))
		copy(entry[IDLen+1:], addrB)
		buf = append(buf, entry...)
	}
	return buf
}

// unmarshalFindNodeResp — то же что parsePeerList (reuse).
var unmarshalFindNodeResp = parsePeerList
