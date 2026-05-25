package swarmproto

import (
	"crypto/cipher"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// SessionCipher создаёт AEAD шифр ChaCha20-Poly1305 из общего секрета.
//
// sharedSecret — результат X25519 обмена ключами (32 байта).
// role — "client" или "server" (разные ключи для разных направлений).
//
// Использует HKDF с SHA-256 для детерминированного вывода ключей.
// Это стандартный паттерн TLS 1.3.
func SessionCipher(sharedSecret []byte, role string) (cipher.AEAD, error) {
	// HKDF: Extract + Expand
	// info содержит роль чтобы client и server получили разные ключи
	info := []byte("swarm-v1-session-" + role)
	r := hkdf.New(sha256.New, sharedSecret, []byte("swarm-v1-salt"), info)

	key := make([]byte, chacha20poly1305.KeySize) // 32 байта
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("HKDF derive: %w", err)
	}

	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("chacha20poly1305.New: %w", err)
	}

	// Обнуляем ключ в памяти после создания шифра
	for i := range key {
		key[i] = 0
	}

	return aead, nil
}

// UpdateCipher создаёт AEAD для шифрования обновлений ноды.
// Отдельный ключ с другим salt для изоляции.
func UpdateCipher(masterKey []byte) (cipher.AEAD, error) {
	r := hkdf.New(sha256.New, masterKey,
		[]byte("swarm-v1-update-salt"),
		[]byte("swarm-v1-update"))

	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("HKDF derive update: %w", err)
	}

	return chacha20poly1305.New(key)
}
