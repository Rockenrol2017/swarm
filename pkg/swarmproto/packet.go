// Package swarmproto — бинарный протокол роя S.W.A.R.M.
//
// Формат кадра (снаружи выглядит как случайный мусор):
//
//	┌──────────┬─────────┬──────────┬─────────────────┬──────────────┐
//	│ 4 bytes  │ 4 bytes │ 32 bytes │ N bytes         │ 16 bytes     │
//	│ noise    │ length  │ nonce    │ ciphertext      │ Poly1305 MAC │
//	└──────────┴─────────┴──────────┴─────────────────┴──────────────┘
//
// Plaintext (внутри зашифрованного payload):
//
//	┌─────────┬──────────┬──────────────┬──────────┐
//	│ 1 byte  │ 8 bytes  │ 32 bytes     │ N bytes  │
//	│ version │ unixNano │ session ID   │ data     │
//	└─────────┴──────────┴──────────────┴──────────┘
//
// Нет magic bytes. Нет сигнатур. Нельзя отличить от случайного трафика.
package swarmproto

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	Version = 1

	// Размеры полей внешнего кадра
	NoiseSize     = 4  // случайный шум в начале — DPI затруднение
	LengthSize    = 4  // длина ciphertext
	// NonceField = CryptoNonceSize + ObfNonceSize — поле nonce в wire format (32 байта total).
	// ChaCha20-Poly1305 использует только первые 12 байт (CryptoNonceSize).
	// Оставшиеся 20 байт (ObfNonceSize) — случайные, увеличивают энтропию кадра.
	// Итого 32 байта random в nonce-поле делают трафик неотличимым от шума.
	CryptoNonceSize = 12 // cipher.NonceSize() для ChaCha20-Poly1305
	ObfNonceSize    = 20 // дополнительная случайная энтропия
	NonceSize       = CryptoNonceSize + ObfNonceSize // 32 байта в wire format
	MACSize         = 16
	FrameOverhead   = NoiseSize + LengthSize + NonceSize + MACSize

	// Размеры полей внутри plaintext payload
	VersionSize   = 1
	TimestampSize = 8
	SessionIDSize = 32
	HeaderSize    = VersionSize + TimestampSize + SessionIDSize

	// Допустимое расхождение часов между узлами.
	// 5 минут — стандарт Kerberos/NTP. Защищает от replay-атак,
	// при этом не ломается на свежих VPS где NTP ещё не успел синхронизироваться.
	MaxClockSkew = 5 * time.Minute

	// Максимальный размер одного кадра (1MB)
	MaxFrameSize = 1 << 20
)

// Frame — распакованный кадр протокола.
type Frame struct {
	SessionID [SessionIDSize]byte
	Timestamp time.Time
	Data      []byte
}

// Encode сериализует Frame в зашифрованный бинарный кадр.
// cipher — AEAD шифр (ChaCha20-Poly1305 или совместимый).
func Encode(f *Frame, cipher AEAD) ([]byte, error) {
	// Собираем plaintext: version + timestamp + sessionID + data
	plain := make([]byte, HeaderSize+len(f.Data))
	plain[0] = Version
	binary.BigEndian.PutUint64(plain[1:9], uint64(f.Timestamp.UnixNano()))
	copy(plain[9:41], f.SessionID[:])
	copy(plain[41:], f.Data)

	// Генерируем nonce: первые CryptoNonceSize байт используются шифром,
	// оставшиеся ObfNonceSize — случайная энтропия для затруднения DPI.
	var nonce [NonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("rand nonce: %w", err)
	}

	// Шифруем: явно используем только первые CryptoNonceSize байт
	ciphertext := cipher.Seal(nil, nonce[:CryptoNonceSize], plain, nil)

	// Случайный шум в начале кадра
	var noise [NoiseSize]byte
	if _, err := rand.Read(noise[:]); err != nil {
		return nil, fmt.Errorf("rand noise: %w", err)
	}

	// Собираем итоговый кадр
	totalLen := FrameOverhead + len(ciphertext)
	if totalLen > MaxFrameSize {
		return nil, errors.New("frame too large")
	}

	frame := make([]byte, totalLen)
	copy(frame[:NoiseSize], noise[:])
	binary.BigEndian.PutUint32(frame[NoiseSize:NoiseSize+LengthSize], uint32(len(ciphertext)))
	copy(frame[NoiseSize+LengthSize:NoiseSize+LengthSize+NonceSize], nonce[:])
	copy(frame[NoiseSize+LengthSize+NonceSize:], ciphertext)

	return frame, nil
}

// Decode расшифровывает бинарный кадр в Frame.
// Проверяет версию и допустимость временной метки.
func Decode(raw []byte, cipher AEAD) (*Frame, error) {
	if len(raw) < FrameOverhead+HeaderSize {
		return nil, errors.New("frame too short")
	}

	// Читаем длину ciphertext (пропускаем noise)
	ctLen := int(binary.BigEndian.Uint32(raw[NoiseSize : NoiseSize+LengthSize]))
	if ctLen <= 0 || FrameOverhead+ctLen > MaxFrameSize {
		return nil, errors.New("invalid ciphertext length")
	}
	if len(raw) < NoiseSize+LengthSize+NonceSize+ctLen {
		return nil, errors.New("truncated frame")
	}

	// Извлекаем nonce и ciphertext
	nonce := raw[NoiseSize+LengthSize : NoiseSize+LengthSize+NonceSize]
	ciphertext := raw[NoiseSize+LengthSize+NonceSize : NoiseSize+LengthSize+NonceSize+ctLen]

	// Расшифровываем: используем только первые CryptoNonceSize байт
	plain, err := cipher.Open(nil, nonce[:CryptoNonceSize], ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	if len(plain) < HeaderSize {
		return nil, errors.New("plaintext too short")
	}

	// Проверяем версию
	if plain[0] != Version {
		return nil, fmt.Errorf("unsupported version: %d", plain[0])
	}

	// Проверяем временную метку (защита от replay атак)
	tsNano := binary.BigEndian.Uint64(plain[1:9])
	ts := time.Unix(0, int64(tsNano))
	skew := time.Since(ts)
	if skew < -MaxClockSkew || skew > MaxClockSkew {
		return nil, fmt.Errorf("clock skew too large: %v", skew)
	}

	// Извлекаем session ID и данные
	var sessionID [SessionIDSize]byte
	copy(sessionID[:], plain[9:41])

	data := make([]byte, len(plain)-HeaderSize)
	copy(data, plain[HeaderSize:])

	return &Frame{
		SessionID: sessionID,
		Timestamp: ts,
		Data:      data,
	}, nil
}

// NewSessionID генерирует случайный идентификатор сессии.
func NewSessionID() ([SessionIDSize]byte, error) {
	var id [SessionIDSize]byte
	_, err := rand.Read(id[:])
	return id, err
}

// AEAD — интерфейс для AEAD шифра (совместим с cipher.AEAD).
type AEAD interface {
	NonceSize() int
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
}
