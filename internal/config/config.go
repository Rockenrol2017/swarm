package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config — конфигурация swarm-core.
// Загружается из /etc/swarm/config.json при старте.
type Config struct {
	// VLESS подключение к VDS
	VlessUUID   string `json:"vless_uuid"`
	VlessServer string `json:"vless_server"` // "IP:PORT"
	VdsIP       string `json:"vds_ip"`       // для iptables правил
	VdsPort     int    `json:"vds_port"`

	// Reality настройки
	SNI       string `json:"sni"`        // www.microsoft.com
	PublicKey string `json:"public_key"` // Reality public key
	ShortId   string `json:"short_id"`   // Reality short id

	// TUN интерфейс
	TunName string `json:"tun_name"` // "swarm0"
	TunIP   string `json:"tun_ip"`   // "10.0.0.1"

	// Порты сервисов
	Socks5Port int `json:"socks5_port"` // 1080
	WebPort    int `json:"web_port"`    // 8080

	// КРИТИЧНО: шлюз по умолчанию сохраняется ДО изменения маршрутов
	// Без этого после включения kill switch потеряем доступ к VDS
	DefaultGW    string `json:"default_gw"`    // "192.168.1.1"
	DefaultIface string `json:"default_iface"` // "eth0"
}

// DefaultConfig возвращает конфигурацию по умолчанию.
// Используется как основа при создании нового конфига.
func DefaultConfig() *Config {
	return &Config{
		TunName:    "swarm0",
		TunIP:      "10.0.0.1",
		Socks5Port: 1080,
		WebPort:    8080,
		SNI:        "www.microsoft.com",
	}
}

// Load загружает конфигурацию из JSON файла.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать конфиг %s: %w", path, err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("ошибка парсинга конфига: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("невалидный конфиг: %w", err)
	}

	return cfg, nil
}

// Validate проверяет обязательные поля конфигурации.
func (c *Config) Validate() error {
	if c.VlessUUID == "" {
		return fmt.Errorf("vless_uuid обязателен")
	}
	if c.VlessServer == "" {
		return fmt.Errorf("vless_server обязателен")
	}
	if c.VdsIP == "" {
		return fmt.Errorf("vds_ip обязателен")
	}
	if c.VdsPort == 0 {
		return fmt.Errorf("vds_port обязателен")
	}
	if c.PublicKey == "" {
		return fmt.Errorf("public_key (Reality) обязателен")
	}
	if c.DefaultGW == "" {
		return fmt.Errorf("default_gw обязателен (шлюз должен быть сохранён до изменения маршрутов)")
	}
	if c.DefaultIface == "" {
		return fmt.Errorf("default_iface обязателен")
	}
	return nil
}

// Save сохраняет конфигурацию в JSON файл.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("ошибка сериализации конфига: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}
