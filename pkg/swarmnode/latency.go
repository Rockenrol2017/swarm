package swarmnode

// latency.go — периодическое измерение RTT до bootstrap сервера.
//
// Метод: HTTP GET /health до VDS:19090 — TCP + HTTP round-trip.
// Даёт реальное время до VDS через спутниковый канал (~600-800мс норма).
//
// Хранится в Node.latencyMs (atomic.Int64):
//   > 0  = RTT в миллисекундах
//   -1   = недоступен (timeout или ошибка)
//    0   = ещё не измерено (начальное состояние)
//
// Интервал: 30 секунд (не нагружать канал лишними запросами).
// Timeout: 8 секунд (спутниковый канал может долго отвечать).

import (
	"net/http"
	"strings"
	"time"
)

// runLatencyProbe измеряет RTT до bootstrap и хранит результат в n.latencyMs.
// Запускается горутиной в Start() только для client режима.
func (n *Node) runLatencyProbe() {
	addr := n.cfg.BootstrapAddr
	if addr == "" {
		return
	}

	// Извлекаем хост из "ip:port"
	host := addr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		host = addr[:i]
	}
	probeURL := "http://" + host + ":19090/health"
	client := &http.Client{
		Timeout: 8 * time.Second,
		// Не следуем редиректам — нам нужен только первый ответ
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	probe := func() {
		start := time.Now()
		resp, err := client.Get(probeURL)
		if err != nil {
			n.latencyMs.Store(-1) // недоступен
			return
		}
		resp.Body.Close()
		rtt := time.Since(start).Milliseconds()
		n.latencyMs.Store(rtt)
	}

	probe() // сразу при старте, не ждём первый тик
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			go probe() // не блокируем основной тикер если спутник медленный
		}
	}
}
