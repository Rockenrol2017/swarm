package swarmnode

// ratelimit.go — rate limiting на двух уровнях.
//
// Уровень 1: IP-level limiter (ipRateLimiter) — ПЕРЕД handshake.
//   Защищает от CPU-exhaustion через дорогостоящий X25519+Ed25519 handshake.
//   5 новых соединений/сек на IP, burst 10.
//   Применяется в handleIncoming() ДО performServerHandshake().
//
// Уровень 2: Stream-level limiters — ПОСЛЕ handshake.
//   Защищает relay от DoS через тысячи MsgConnect.
//   - Глобальный (Node.relayLimiter): 50 потоков/сек, burst 150
//   - Пер-пир (Peer.connLimiter): 10 потоков/сек, burst 30
//
// Реализация — классический token bucket без внешних зависимостей.

import (
	"net"
	"sync"
	"time"
)

// tokenBucket — потокобезопасный token bucket.
type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	rate     float64 // токенов в секунду
	capacity float64 // максимум токенов (burst)
	last     time.Time
}

// newTokenBucket создаёт token bucket с заданным rate и burst.
func newTokenBucket(rate, burst float64) *tokenBucket {
	return &tokenBucket{
		tokens:   burst,
		rate:     rate,
		capacity: burst,
		last:     time.Now(),
	}
}

// Allow возвращает true если запрос разрешён (тратит 1 токен).
func (tb *tokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.last).Seconds()
	tb.last = now

	// Пополняем токены пропорционально прошедшему времени
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}

	if tb.tokens < 1.0 {
		return false
	}
	tb.tokens -= 1.0
	return true
}

// ─── IP-level rate limiter ────────────────────────────────────────────────────

// ipEntry — запись о конкретном IP.
type ipEntry struct {
	bucket  *tokenBucket
	lastSee time.Time
}

// ipRateLimiter — per-IP rate limiter с автоочисткой устаревших записей.
// Применяется ДО handshake чтобы блокировать CPU-exhaustion атаки.
type ipRateLimiter struct {
	mu      sync.Mutex
	entries map[string]*ipEntry
	rate    float64
	burst   float64
}

// newIPRateLimiter создаёт IP-level limiter.
// rate — новых соединений/сек на один IP, burst — максимальный всплеск.
func newIPRateLimiter(rate, burst float64) *ipRateLimiter {
	rl := &ipRateLimiter{
		entries: make(map[string]*ipEntry),
		rate:    rate,
		burst:   burst,
	}
	go rl.cleanupLoop()
	return rl
}

// Allow проверяет лимит для удалённого адреса (net.Addr или строка "ip:port").
// Возвращает false если IP превысил лимит.
func (rl *ipRateLimiter) Allow(addr net.Addr) bool {
	ip := extractIP(addr)

	rl.mu.Lock()
	e, ok := rl.entries[ip]
	if !ok {
		e = &ipEntry{bucket: newTokenBucket(rl.rate, rl.burst)}
		rl.entries[ip] = e
	}
	e.lastSee = time.Now()
	rl.mu.Unlock()

	return e.bucket.Allow()
}

// cleanupLoop удаляет записи для IP которые не активны > 10 минут.
func (rl *ipRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		rl.mu.Lock()
		for ip, e := range rl.entries {
			if e.lastSee.Before(cutoff) {
				delete(rl.entries, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// extractIP извлекает IP-адрес без порта из net.Addr.
func extractIP(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String() // fallback: весь адрес как ключ
	}
	return host
}
