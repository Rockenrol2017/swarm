package swarmnode

// dht.go — персистентный кэш известных пиров (DHT-заглушка).
//
// Это не полноценный Kademlia DHT, а простой кэш: при каждом подключении
// адрес пира сохраняется в peers.json. При следующем старте узел может
// подключиться к известным пирам без bootstrap сервера.
//
// Иерархия приоритетов при старте (connectToBootstrapOrCache):
//   1. bootstrap_addrs из конфига (стандартный путь)
//   2. peers.json на диске (если конфиг пустой)
//   3. fallbackBootstrapAddrs — hardcoded в коде (последний рубеж)
//
// Это позволяет рою выживать даже если все VDS упали:
//   Узел A подключился к Bootstrap → сохранил IP узла B →
//   Bootstrap упал → Узел A перезапустился → подключился к B напрямую.

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Rockenrol2017/swarm/pkg/swarmproto"
)

// fallbackBootstrapAddrs — hardcoded адреса bootstrap нод.
// Используются ТОЛЬКО если конфиг пустой И peers.json пуст.
// Это последний рубеж — рой не умирает полностью.
var fallbackBootstrapAddrs = []string{
	"193.68.89.168:7437", // Stockholm, SE (hip.hosting)
	"78.17.74.239:7437",  // Frankfurt, DE (One Dash)
	"166.1.89.52:7437",   // New York, US (One Dash)
}

// PeerRecord — информация об известном пире для сохранения на диск.
type PeerRecord struct {
	Addr      string `json:"addr"`                 // "ip:port"
	NodeID    string `json:"node_id,omitempty"`    // hex Ed25519 pubkey
	Country   string `json:"country,omitempty"`    // "DE", "SE", ...
	LastSeen  int64  `json:"last_seen"`            // unix timestamp
	LatencyMs int64  `json:"latency_ms,omitempty"` // последний измеренный RTT
}

// peerCacheFile — структура файла peers.json на диске.
type peerCacheFile struct {
	Peers     []*PeerRecord `json:"peers"`
	UpdatedAt int64         `json:"updated_at"`
}

// signedCache — обёртка для подписанного peers.json.
// Data содержит JSON-сериализацию peerCacheFile.
// Signature — hex(Ed25519Sign(identity.PrivKey, Data)).
type signedCache struct {
	Data      json.RawMessage `json:"data"`
	Signature string          `json:"signature,omitempty"`
}

// PeerCache — поточно-безопасный кэш известных пиров с персистентностью.
// Файл peers.json подписывается Ed25519 ключом узла для защиты от
// MITM-подмены (злоумышленник не может подсунуть чужие адреса).
type PeerCache struct {
	mu       sync.RWMutex
	records  map[string]*PeerRecord // key: addr
	filePath string
	maxSize  int                         // максимум записей (500)
	identity *swarmproto.NodeIdentity    // для подписи/проверки; nil = без подписи
}

// newPeerCache создаёт новый кэш.
// identity может быть nil — тогда подпись не используется.
func newPeerCache(filePath string, maxSize int, identity *swarmproto.NodeIdentity) *PeerCache {
	return &PeerCache{
		records:  make(map[string]*PeerRecord),
		filePath: filePath,
		maxSize:  maxSize,
		identity: identity,
	}
}

// Load загружает кэш с диска.
// Если identity установлен — проверяет Ed25519 подпись файла.
// Для обратной совместимости принимает файлы в старом формате (без обёртки signedCache)
// — в этом случае предупреждает но не падает, чтобы апгрейд не ломал рой.
func (c *PeerCache) Load() error {
	raw, err := os.ReadFile(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // нормально — первый запуск
		}
		return err
	}

	// Пытаемся разобрать как signedCache
	var sc signedCache
	if jsonErr := json.Unmarshal(raw, &sc); jsonErr != nil {
		return fmt.Errorf("parse peers.json: %w", jsonErr)
	}

	// Backward compatibility: старый формат не имел поля "data",
	// в нём сразу шли "peers" и "updated_at".
	if sc.Data == nil {
		// Старый незащищённый формат — принимаем один раз, при следующем Save подпишем.
		log.Printf("[dht] peers.json: старый формат (без подписи), принимаем для миграции")
		var f peerCacheFile
		if err := json.Unmarshal(raw, &f); err != nil {
			return fmt.Errorf("parse peers.json (legacy): %w", err)
		}
		c.mu.Lock()
		for _, r := range f.Peers {
			if r.Addr != "" {
				c.records[r.Addr] = r
			}
		}
		c.mu.Unlock()
		return nil
	}

	if c.identity != nil {
		// Проверяем подпись
		if sc.Signature == "" {
			// Файл в новом формате но без подписи — предупреждаем, принимаем
			log.Printf("[dht] peers.json: подпись отсутствует (миграция?), принимаем")
		} else {
			sig, err := hex.DecodeString(sc.Signature)
			if err != nil {
				return fmt.Errorf("peers.json: неверный формат подписи: %w", err)
			}
			if !ed25519.Verify(c.identity.PubKey, sc.Data, sig) {
				return errors.New("peers.json: неверная подпись — файл отклонён")
			}
		}
	}

	var f peerCacheFile
	if err := json.Unmarshal(sc.Data, &f); err != nil {
		return fmt.Errorf("parse peers data: %w", err)
	}

	c.mu.Lock()
	for _, r := range f.Peers {
		if r.Addr != "" {
			c.records[r.Addr] = r
		}
	}
	c.mu.Unlock()
	return nil
}

// Save сохраняет кэш на диск.
// Если identity установлен — подписывает файл Ed25519 ключом узла.
func (c *PeerCache) Save() error {
	c.mu.RLock()
	records := make([]*PeerRecord, 0, len(c.records))
	for _, r := range c.records {
		records = append(records, r)
	}
	c.mu.RUnlock()

	f := peerCacheFile{
		Peers:     records,
		UpdatedAt: time.Now().Unix(),
	}

	// Сериализуем данные (payload для подписи)
	payload, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}

	sc := signedCache{
		Data: json.RawMessage(payload),
	}

	// Подписываем если есть identity
	if c.identity != nil {
		sig := ed25519.Sign(c.identity.PrivKey, payload)
		sc.Signature = hex.EncodeToString(sig)
	}

	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}

	// Атомарная запись через temp файл
	tmp := c.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, c.filePath)
}

// Add добавляет или обновляет запись о пире.
func (c *PeerCache) Add(addr, nodeID string) {
	if addr == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if r, ok := c.records[addr]; ok {
		// Обновляем существующую запись
		r.LastSeen = time.Now().Unix()
		if nodeID != "" {
			r.NodeID = nodeID
		}
		return
	}

	// Если достигли лимита — удаляем самого старого пира
	if len(c.records) >= c.maxSize {
		var oldest string
		var oldestTime int64 = 1<<62
		for k, r := range c.records {
			if r.LastSeen < oldestTime {
				oldestTime = r.LastSeen
				oldest = k
			}
		}
		delete(c.records, oldest)
	}

	c.records[addr] = &PeerRecord{
		Addr:     addr,
		NodeID:   nodeID,
		LastSeen: time.Now().Unix(),
	}
}

// KnownAddrs возвращает список всех известных адресов для подключения.
// Отсортированы по last_seen (свежие первые).
func (c *PeerCache) KnownAddrs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	addrs := make([]string, 0, len(c.records))
	for addr := range c.records {
		addrs = append(addrs, addr)
	}
	return addrs
}

// Len возвращает количество записей в кэше.
func (c *PeerCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.records)
}

// startPeriodicSave запускает горутину периодического сохранения (каждые 5 минут).
func (c *PeerCache) startPeriodicSave(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Save(); err != nil {
				log.Printf("[dht] ошибка сохранения peer cache: %v", err)
			} else {
				log.Printf("[dht] peer cache сохранён (%d записей)", c.Len())
			}
		}
	}
}
