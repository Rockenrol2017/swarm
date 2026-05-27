package swarmnode

// geo.go — геолокация IP и гео-маршрутизация трафика.
//
// Определение страны по IP:
//   API: https://ipapi.co/{ip}/json/ (timeout 3с)
//   Кэш: in-memory, 24 часа TTL (экономия трафика на спутнике)
//
// Тип IP (residential vs datacenter):
//   Проверяется по ASN org из ipapi.co
//   Известные датацентры: Hetzner, OVH, AWS, DigitalOcean, Vultr, Linode, etc.
//
// Гео-маршрутизация (selectPeerForDomain в node.go):
//   Для доменов из geoContentRules → выбираем пир с нужной страной или типом.
//   Если нет подходящего пира → обычный selectPeer().
//
// Производительность:
//   Спутник: RTT 1800мс → API вызовы кэшируются на 24ч.
//   Pentium B960: lookup без вычислений → только map lookup.
//   Geo lookup асинхронен (горутина) — не блокирует handshake.

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// geoEntry — запись в кэше геолокации.
type geoEntry struct {
	country string // ISO 3166-1 alpha-2 ("DE", "SE", "US", ...)
	ipType  string // "residential" | "datacenter" | ""
	expires time.Time
}

var (
	geoMu    sync.RWMutex
	geoCache = make(map[string]*geoEntry) // key: IP адрес
)

// geoAPIResponse — ответ от ipapi.co/json/
type geoAPIResponse struct {
	CountryCode string `json:"country_code"`
	Org         string `json:"org"` // "AS24940 Hetzner Online GmbH"
}

// datacenterASNs — ключевые слова в ASN org которые указывают на датацентр.
var datacenterASNs = []string{
	"hetzner", "ovh", "amazon", "aws", "digitalocean", "vultr", "linode",
	"akamai", "cloudflare", "google", "microsoft", "azure", "fastly",
	"leaseweb", "contabo", "netcup", "ionos", "hosting", "datacenter",
	"colocation", "server", "vps", "cloud", "dedicated",
}

// lookupGeo определяет страну и тип IP.
// Результат кэшируется на 24 часа.
// Возвращает ("", "") при ошибке или для приватных IP.
func lookupGeo(ip string) (country, ipType string) {
	// Пропускаем приватные и loopback адреса
	if isPrivateIP(ip) {
		return "", ""
	}

	// Проверяем кэш
	geoMu.RLock()
	entry, ok := geoCache[ip]
	geoMu.RUnlock()

	if ok && time.Now().Before(entry.expires) {
		return entry.country, entry.ipType
	}

	// Запрашиваем API (timeout 3s — не блокируем надолго на спутнике)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("https://ipapi.co/" + ip + "/json/")
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", ""
	}

	var apiResp geoAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		log.Printf("[geo] ошибка парсинга ответа для %s: %v", ip, err)
		return "", ""
	}

	// Определяем тип IP по ASN org
	orgLower := strings.ToLower(apiResp.Org)
	ipTypeResult := "residential"
	for _, keyword := range datacenterASNs {
		if strings.Contains(orgLower, keyword) {
			ipTypeResult = "datacenter"
			break
		}
	}

	// Сохраняем в кэш на 24 часа
	newEntry := &geoEntry{
		country: apiResp.CountryCode,
		ipType:  ipTypeResult,
		expires: time.Now().Add(24 * time.Hour),
	}

	geoMu.Lock()
	geoCache[ip] = newEntry
	geoMu.Unlock()

	return apiResp.CountryCode, ipTypeResult
}

// isPrivateIP проверяет является ли IP приватным/loopback.
func isPrivateIP(ip string) bool {
	if ip == "" || ip == "127.0.0.1" || ip == "::1" {
		return true
	}
	// Приватные диапазоны
	private := []string{"10.", "192.168.", "172.16.", "172.17.", "172.18.",
		"172.19.", "172.20.", "172.21.", "172.22.", "172.23.", "172.24.",
		"172.25.", "172.26.", "172.27.", "172.28.", "172.29.", "172.30.",
		"172.31.", "fd", "fe80", "::"}
	for _, prefix := range private {
		if strings.HasPrefix(ip, prefix) {
			return true
		}
	}
	return false
}

// ─── Гео-контент маршрутизация ────────────────────────────────────────────

// geoContentRules — база гео-ограниченного контента.
// Ключ: суффикс домена (без www и субдоменов).
// Значение: список правил ["US", "GB", "residential", ...].
// "residential" — любая страна, но только жилые IP (не датацентры).
var geoContentRules = map[string][]string{
	// США
	"hulu.com":       {"US"},
	"peacocktv.com":  {"US"},
	"paramountplus.com": {"US"},
	"fubo.tv":        {"US"},
	"espn.com":       {"US"},

	// Великобритания
	"bbc.co.uk":      {"GB"},
	"itvx.com":       {"GB"},
	"channel4.com":   {"GB"},

	// Другие
	"tvn.hu":         {"HU"},
	"weibo.com":      {"CN"},
	"bilibili.com":   {"CN"},
	"nicovideo.jp":   {"JP"},
	"abema.tv":       {"JP"},

	// Residential priority (Netflix/Spotify блокируют датацентры)
	"netflix.com":    {"residential"},
	"spotify.com":    {"residential"},
	"disneyplus.com": {"residential"},
	"hbomax.com":     {"US", "residential"},
}

// geoRulesForDomain возвращает правила маршрутизации для домена.
// Проверяет точное совпадение и суффиксы (sub.domain.com → domain.com).
func geoRulesForDomain(domain string) []string {
	// Убираем порт если есть
	if i := strings.LastIndex(domain, ":"); i > 0 {
		domain = domain[:i]
	}
	// Убираем www.
	domain = strings.TrimPrefix(domain, "www.")

	// Точное совпадение
	if rules, ok := geoContentRules[domain]; ok {
		return rules
	}

	// Суффикс: "sub.domain.com" → проверяем "domain.com"
	parts := strings.SplitN(domain, ".", 2)
	if len(parts) == 2 {
		if rules, ok := geoContentRules[parts[1]]; ok {
			return rules
		}
	}

	return nil
}
