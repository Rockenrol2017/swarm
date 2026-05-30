package swarmnode

// ratelimit.go — простой token bucket для защиты relay от DoS.
//
// Проблема: злоумышленник может слать тысячи MsgConnect подряд, заставляя
// relay-узел открывать тысячи TCP-соединений и расходовать все ресурсы.
//
// Решение: два уровня rate limiting:
//   1. Глобальный (Node.relayLimiter) — суммарный поток от всех пиров.
//   2. Пер-пир (Peer.connLimiter) — изолирует одного агрессивного клиента.
//
// Параметры выбраны под реальный трафик:
//   - 50 потоков/сек глобально, burst 150 — достаточно для нормальной работы
//   - 10 потоков/сек на пира, burst 30 — блокирует flood от одной ноды
//
// Реализация — классический leaky bucket без внешних зависимостей.

import (
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
