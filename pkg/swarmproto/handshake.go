package swarmproto

import (
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Handshake — полный протокол рукопожатия.
//
// Схема (аналог TLS 1.3 с нашим форматом):
//
//	Client → Server: ClientHello
//	  [32] X25519 ephemeral pubkey
//	  [32] Ed25519 node ID (pubkey)
//	  [64] Ed25519 подпись(ephemeral_pubkey + timestamp)
//	  [8]  timestamp (unixNano)
//
//	Server → Client: ServerHello
//	  [32] X25519 ephemeral pubkey
//	  [32] Ed25519 node ID
//	  [64] Ed25519 подпись(ephemeral_pubkey + client_ephemeral + timestamp)
//	  [8]  timestamp
//
//	После обмена:
//	  sharedSecret = X25519(client_priv, server_pub) = X25519(server_priv, client_pub)
//	  sessionKey = HKDF(sharedSecret, ...)

const (
	// ProtoVersion — текущая версия протокола рукопожатия.
	// Увеличивать при несовместимых изменениях формата Hello.
	ProtoVersion uint8 = 1

	// HelloSize — размер сериализованного ClientHello / ServerHello.
	// Layout: [1 version][32 ephemeral][32 nodeID][64 sig][8 timestamp]
	HelloSize = 1 + 32 + 32 + 64 + 8 // 137 байт
)

// NodeIdentity — долгосрочная идентификация узла в сети.
type NodeIdentity struct {
	// Ed25519 ключевая пара для подписей (подписываем рукопожатия, объявления)
	PrivKey ed25519.PrivateKey
	PubKey  ed25519.PublicKey // NodeID — уникальный идентификатор узла
}

// GenerateIdentity создаёт новую идентификацию узла.
// Вызывается один раз при первом запуске, потом сохраняется на диск.
func GenerateIdentity() (*NodeIdentity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ed25519 keygen: %w", err)
	}
	return &NodeIdentity{PrivKey: priv, PubKey: pub}, nil
}

// NodeID возвращает 32-байтовый идентификатор узла (Ed25519 pubkey).
func (n *NodeIdentity) NodeID() [32]byte {
	var id [32]byte
	copy(id[:], n.PubKey)
	return id
}

// ClientHello — данные рукопожатия от клиента.
type ClientHello struct {
	Version      uint8    // ProtoVersion — версия протокола
	EphemeralPub [32]byte // X25519 ephemeral pubkey
	NodeID       [32]byte // Ed25519 pubkey клиента
	Signature    [64]byte // Ed25519(EphemeralPub || timestamp)
	Timestamp    int64    // UnixNano
}

// ServerHello — ответ сервера.
type ServerHello struct {
	Version      uint8    // ProtoVersion — версия протокола
	EphemeralPub [32]byte // X25519 ephemeral pubkey
	NodeID       [32]byte // Ed25519 pubkey сервера
	Signature    [64]byte // Ed25519(EphemeralPub || clientEphemeral || timestamp)
	Timestamp    int64
}

// HandshakeResult — результат успешного рукопожатия.
type HandshakeResult struct {
	PeerNodeID   [32]byte     // Идентификатор собеседника
	SendCipher   cipher.AEAD  // Шифр для отправки
	RecvCipher   cipher.AEAD  // Шифр для приёма
}

// BuildClientHello создаёт ClientHello.
// Возвращает hello, ephemeral private key (нужен для вычисления shared secret).
func BuildClientHello(id *NodeIdentity) (*ClientHello, *ecdh.PrivateKey, error) {
	// Генерируем ephemeral X25519 ключ
	ephPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("X25519 keygen: %w", err)
	}

	hello := &ClientHello{
		Version:   ProtoVersion,
		Timestamp: time.Now().UnixNano(),
		NodeID:    id.NodeID(),
	}
	copy(hello.EphemeralPub[:], ephPriv.PublicKey().Bytes())

	// Подписываем: ephemeral_pub + timestamp
	msg := make([]byte, 32+8)
	copy(msg[:32], hello.EphemeralPub[:])
	binary.BigEndian.PutUint64(msg[32:], uint64(hello.Timestamp))
	sig := ed25519.Sign(id.PrivKey, msg)
	copy(hello.Signature[:], sig)

	return hello, ephPriv, nil
}

// VerifyClientHello проверяет версию протокола и подпись ClientHello.
func VerifyClientHello(hello *ClientHello) error {
	// Проверяем версию протокола
	if hello.Version != ProtoVersion {
		return fmt.Errorf("unsupported proto version: got %d, want %d", hello.Version, ProtoVersion)
	}

	// Проверяем временную метку
	ts := time.Unix(0, hello.Timestamp)
	if skew := time.Since(ts).Abs(); skew > MaxClockSkew {
		return fmt.Errorf("clock skew %v > %v", skew, MaxClockSkew)
	}

	// Восстанавливаем сообщение которое было подписано
	msg := make([]byte, 32+8)
	copy(msg[:32], hello.EphemeralPub[:])
	binary.BigEndian.PutUint64(msg[32:], uint64(hello.Timestamp))

	if !ed25519.Verify(hello.NodeID[:], msg, hello.Signature[:]) {
		return errors.New("invalid client signature")
	}
	return nil
}

// BuildServerHello создаёт ServerHello и вычисляет shared secret.
func BuildServerHello(id *NodeIdentity, clientHello *ClientHello) (
	*ServerHello, *HandshakeResult, error) {

	if err := VerifyClientHello(clientHello); err != nil {
		return nil, nil, fmt.Errorf("verify client hello: %w", err)
	}

	// Ephemeral ключ сервера
	ephPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("X25519 keygen: %w", err)
	}

	hello := &ServerHello{
		Version:   ProtoVersion,
		Timestamp: time.Now().UnixNano(),
		NodeID:    id.NodeID(),
	}
	copy(hello.EphemeralPub[:], ephPriv.PublicKey().Bytes())

	// Подписываем: server_eph + client_eph + timestamp
	msg := make([]byte, 32+32+8)
	copy(msg[:32], hello.EphemeralPub[:])
	copy(msg[32:64], clientHello.EphemeralPub[:])
	binary.BigEndian.PutUint64(msg[64:], uint64(hello.Timestamp))
	sig := ed25519.Sign(id.PrivKey, msg)
	copy(hello.Signature[:], sig)

	// Вычисляем shared secret
	clientPub, err := ecdh.X25519().NewPublicKey(clientHello.EphemeralPub[:])
	if err != nil {
		return nil, nil, fmt.Errorf("parse client pubkey: %w", err)
	}

	shared, err := ephPriv.ECDH(clientPub)
	if err != nil {
		return nil, nil, fmt.Errorf("X25519 ECDH: %w", err)
	}

	result, err := deriveSessionCiphers(shared, clientHello.EphemeralPub, hello.EphemeralPub, false)
	if err != nil {
		return nil, nil, err
	}
	result.PeerNodeID = clientHello.NodeID

	return hello, result, nil
}

// FinalizeHandshake завершает рукопожатие на стороне клиента.
func FinalizeHandshake(clientEphPriv *ecdh.PrivateKey, serverHello *ServerHello) (
	*HandshakeResult, error) {

	// Проверяем подпись сервера (нужен NodeID сервера который мы уже знаем из конфига)
	// TODO: в полной реализации проверяем NodeID сервера по whitelist/DHT
	ts := time.Unix(0, serverHello.Timestamp)
	if skew := time.Since(ts).Abs(); skew > MaxClockSkew {
		return nil, fmt.Errorf("server clock skew %v", skew)
	}

	// Вычисляем shared secret
	serverPub, err := ecdh.X25519().NewPublicKey(serverHello.EphemeralPub[:])
	if err != nil {
		return nil, fmt.Errorf("parse server pubkey: %w", err)
	}

	clientEphPub := [32]byte{}
	copy(clientEphPub[:], clientEphPriv.PublicKey().Bytes())

	shared, err := clientEphPriv.ECDH(serverPub)
	if err != nil {
		return nil, fmt.Errorf("X25519 ECDH: %w", err)
	}

	result, err := deriveSessionCiphers(shared, clientEphPub, serverHello.EphemeralPub, true)
	if err != nil {
		return nil, err
	}
	result.PeerNodeID = serverHello.NodeID

	return result, nil
}

// deriveSessionCiphers выводит пару шифров из shared secret.
// clientPub и serverPub используются как salt для HKDF чтобы
// разные соединения получали разные ключи даже при одинаковом shared secret.
func deriveSessionCiphers(shared []byte, clientPub, serverPub [32]byte, isClient bool) (
	*HandshakeResult, error) {

	// Соединяем shared + оба pubkey для уникальности
	material := make([]byte, len(shared)+64)
	copy(material[:len(shared)], shared)
	copy(material[len(shared):len(shared)+32], clientPub[:])
	copy(material[len(shared)+32:], serverPub[:])

	var sendRole, recvRole string
	if isClient {
		sendRole, recvRole = "client", "server"
	} else {
		sendRole, recvRole = "server", "client"
	}

	sendCipher, err := SessionCipher(material, sendRole)
	if err != nil {
		return nil, fmt.Errorf("send cipher: %w", err)
	}

	recvCipher, err := SessionCipher(material, recvRole)
	if err != nil {
		return nil, fmt.Errorf("recv cipher: %w", err)
	}

	return &HandshakeResult{
		SendCipher: sendCipher,
		RecvCipher: recvCipher,
	}, nil
}

// MarshalClientHello сериализует ClientHello в байты.
// Layout: [1 version][32 ephemeral][32 nodeID][64 sig][8 timestamp]
func MarshalClientHello(h *ClientHello) []byte {
	buf := make([]byte, HelloSize)
	buf[0] = h.Version
	copy(buf[1:33], h.EphemeralPub[:])
	copy(buf[33:65], h.NodeID[:])
	copy(buf[65:129], h.Signature[:])
	binary.BigEndian.PutUint64(buf[129:], uint64(h.Timestamp))
	return buf
}

// UnmarshalClientHello разбирает байты в ClientHello.
func UnmarshalClientHello(b []byte) (*ClientHello, error) {
	if len(b) < HelloSize {
		return nil, fmt.Errorf("too short: %d < %d", len(b), HelloSize)
	}
	h := &ClientHello{}
	h.Version = b[0]
	copy(h.EphemeralPub[:], b[1:33])
	copy(h.NodeID[:], b[33:65])
	copy(h.Signature[:], b[65:129])
	h.Timestamp = int64(binary.BigEndian.Uint64(b[129:]))
	return h, nil
}

// MarshalServerHello сериализует ServerHello.
// Layout: [1 version][32 ephemeral][32 nodeID][64 sig][8 timestamp]
func MarshalServerHello(h *ServerHello) []byte {
	buf := make([]byte, HelloSize)
	buf[0] = h.Version
	copy(buf[1:33], h.EphemeralPub[:])
	copy(buf[33:65], h.NodeID[:])
	copy(buf[65:129], h.Signature[:])
	binary.BigEndian.PutUint64(buf[129:], uint64(h.Timestamp))
	return buf
}

// UnmarshalServerHello разбирает байты в ServerHello.
func UnmarshalServerHello(b []byte) (*ServerHello, error) {
	if len(b) < HelloSize {
		return nil, fmt.Errorf("too short: %d < %d", len(b), HelloSize)
	}
	h := &ServerHello{}
	h.Version = b[0]
	copy(h.EphemeralPub[:], b[1:33])
	copy(h.NodeID[:], b[33:65])
	copy(h.Signature[:], b[65:129])
	h.Timestamp = int64(binary.BigEndian.Uint64(b[129:]))
	return h, nil
}
