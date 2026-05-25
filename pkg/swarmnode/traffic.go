package swarmnode

// traffic.go — персистентные счётчики трафика.
//
// Цель: мониторинг лимита SkyEdge (35-64 ГБ/мес).
// Счётчики выживают рестарты демона — хранятся в JSON файле.
//
// Структура хранимых данных:
//   bytes_today  — байт за текущий день (сбрасывается в 00:00)
//   bytes_month  — байт за текущий месяц (сбрасывается 1-го числа)
//   month_key    — "2026-05" (для детектирования смены месяца)
//   day_key      — "2026-05-25" (для детектирования смены дня)
//
// Периодическое сохранение: каждые 60 секунд (в runTrafficSaver).
// Принудительное сохранение: при остановке узла (Stop).

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TrafficRecord — накопленный трафик (сохраняется на диск).
type TrafficRecord struct {
	BytesMonth int64  `json:"bytes_month"` // суммарно за текущий месяц
	BytesToday int64  `json:"bytes_today"` // суммарно за текущий день
	BytesUp    int64  `json:"bytes_up"`    // upload за текущий день (клиент→рой)
	BytesDown  int64  `json:"bytes_down"`  // download за текущий день (рой→клиент)
	MonthKey   string `json:"month_key"`   // "2026-05"
	DayKey     string `json:"day_key"`     // "2026-05-25"
	UpdatedAt  int64  `json:"updated_at"`  // unix timestamp последнего обновления
}

// trafficStore — потокобезопасное хранилище счётчиков трафика.
// Автоматически сбрасывает счётчики при смене дня/месяца.
type trafficStore struct {
	mu     sync.Mutex
	record TrafficRecord
	path   string
	dirty  bool
}

// newTrafficStore создаёт хранилище и загружает сохранённые данные.
// Если path == "", работает только в памяти (без персистентности).
func newTrafficStore(path string) *trafficStore {
	ts := &trafficStore{path: path}
	if path != "" {
		ts.load()
	}
	return ts
}

// add добавляет up/down байты к счётчикам трафика.
// Автоматически сбрасывает дневные счётчики при смене дня, месячные — при смене месяца.
func (ts *trafficStore) add(up, down int64) {
	now := time.Now()
	monthKey := now.Format("2006-01")
	dayKey := now.Format("2006-01-02")

	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Сброс месячного счётчика при смене месяца
	if ts.record.MonthKey != monthKey {
		ts.record.BytesMonth = 0
		ts.record.MonthKey = monthKey
	}
	// Сброс дневных счётчиков при смене дня
	if ts.record.DayKey != dayKey {
		ts.record.BytesToday = 0
		ts.record.BytesUp = 0
		ts.record.BytesDown = 0
		ts.record.DayKey = dayKey
	}

	total := up + down
	ts.record.BytesMonth += total
	ts.record.BytesToday += total
	ts.record.BytesUp += up
	ts.record.BytesDown += down
	ts.record.UpdatedAt = now.Unix()
	ts.dirty = true
}

// get возвращает копию текущей записи.
func (ts *trafficStore) get() TrafficRecord {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.record
}

// saveIfDirty сохраняет на диск только если есть несохранённые изменения.
// Вызывается каждые 60 секунд из runTrafficSaver.
func (ts *trafficStore) saveIfDirty() {
	ts.mu.Lock()
	if !ts.dirty {
		ts.mu.Unlock()
		return
	}
	record := ts.record
	ts.dirty = false
	ts.mu.Unlock()
	ts.writeToDisk(record)
}

// save принудительно сохраняет на диск.
// Вызывается при остановке узла (Node.Stop) чтобы не потерять счётчики.
func (ts *trafficStore) save() {
	ts.mu.Lock()
	record := ts.record
	ts.dirty = false
	ts.mu.Unlock()
	ts.writeToDisk(record)
}

func (ts *trafficStore) writeToDisk(record TrafficRecord) {
	if ts.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(ts.path), 0755); err != nil {
		log.Printf("[traffic] mkdir %s: %v", filepath.Dir(ts.path), err)
		return
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(ts.path, data, 0644); err != nil {
		log.Printf("[traffic] сохранение не удалось: %v", err)
	}
}

func (ts *trafficStore) load() {
	data, err := os.ReadFile(ts.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[traffic] ошибка чтения %s: %v", ts.path, err)
		}
		return // файл не существует — это нормально для первого запуска
	}
	var record TrafficRecord
	if err := json.Unmarshal(data, &record); err != nil {
		log.Printf("[traffic] ошибка парсинга: %v", err)
		return
	}
	ts.record = record
	log.Printf("[traffic] Загружен: %s — сегодня %.1f МБ, месяц %.1f МБ",
		record.MonthKey,
		float64(record.BytesToday)/1024/1024,
		float64(record.BytesMonth)/1024/1024)
}
