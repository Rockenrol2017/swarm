package swarmnode

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
)

// socks5Server — встроенный SOCKS5 прокси.
// Принимает трафик от браузеров/приложений и направляет через рой.
type socks5Server struct {
	node *Node
}

// Listen запускает SOCKS5 сервер на указанном адресе.
func (s *socks5Server) Listen(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("socks5 listen %s: %w", addr, err)
	}
	defer ln.Close()

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.Printf("[socks5] accept error: %v", err)
				continue
			}
		}
		go s.handleConn(ctx, conn)
	}
}

// handleConn обрабатывает одно SOCKS5 соединение.
func (s *socks5Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// SOCKS5 negotiation
	target, err := socks5Handshake(conn)
	if err != nil {
		// UDP ASSOCIATE (cmd=3) и BIND (cmd=2) — ожидаемый отказ от xray,
		// не логируем как ошибку чтобы не засорять вывод.
		if _, ok := err.(errUnsupportedCmd); !ok {
			log.Printf("[socks5] handshake: %v", err)
		}
		return
	}

	// Выбираем пир для маршрутизации (гео-маршрутизация → latency-first → round-robin)
	// Извлекаем домен из target для гео-проверки ("host:port" → "host")
	domain := target
	if i := len(target) - 1; i > 0 {
		for j := len(target) - 1; j >= 0; j-- {
			if target[j] == ':' {
				domain = target[:j]
				break
			}
		}
	}
	peer := s.node.selectPeerForDomain(domain)
	if peer == nil {
		log.Printf("[socks5] нет доступных пиров для %s", target)
		// Fallback: прямое подключение
		s.directConnect(conn, target)
		return
	}

	// Открываем поток к пиру
	stream, err := peer.OpenProxyStream(ctx, target)
	if err != nil {
		log.Printf("[socks5] open stream to %s via %s: %v", target, peer.NodeIDShort(), err)
		return
	}
	defer stream.Close()

	log.Printf("[socks5] %s → [%s] → %s (country=%s)", conn.RemoteAddr(), peer.NodeIDShort(), target, peer.Country)

	// Двунаправленная передача с подсчётом байт.
	// io.Copy заменён на ручные циклы чтобы считать трафик для мониторинга SkyEdge.
	done := make(chan struct{}, 2)

	// conn → stream: upload (локальный клиент → рой)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			nr, err := conn.Read(buf)
			if nr > 0 {
				stream.Write(buf[:nr])
				s.node.addProxiedBytes(int64(nr), 0)
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	// stream → conn: download (рой → локальный клиент)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			nr, err := stream.Read(buf)
			if nr > 0 {
				conn.Write(buf[:nr])
				s.node.addProxiedBytes(0, int64(nr))
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	<-done
}

// directConnect — прямое соединение без роя (fallback когда нет пиров).
func (s *socks5Server) directConnect(conn net.Conn, target string) {
	remote, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("[socks5] direct dial %s: %v", target, err)
		return
	}
	defer remote.Close()

	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, remote); done <- struct{}{} }()
	<-done
}

// selectPeer выбирает пир для маршрутизации (round-robin).
// Вызывается из socks5.go и tproxy.go.
// Определена в node.go — selectPeerRoundRobin.

// ─── SOCKS5 protocol ──────────────────────────────────────────────────────

// errUnsupportedCmd — ошибка неподдерживаемой команды SOCKS5 (BIND/UDP ASSOCIATE).
// Используется чтобы отличить её от настоящих ошибок и не загрязнять лог.
type errUnsupportedCmd struct{ cmd byte }

func (e errUnsupportedCmd) Error() string {
	return fmt.Sprintf("unsupported command: %d", e.cmd)
}

// socks5Handshake выполняет SOCKS5 рукопожатие и возвращает "host:port".
func socks5Handshake(conn net.Conn) (string, error) {
	// 1. Читаем версию и методы аутентификации
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", err
	}
	if header[0] != 5 {
		return "", fmt.Errorf("not SOCKS5: version %d", header[0])
	}

	nmethods := int(header[1])
	methods := make([]byte, nmethods)
	io.ReadFull(conn, methods)

	// Отвечаем: no authentication required
	conn.Write([]byte{5, 0})

	// 2. Читаем запрос
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		return "", err
	}
	if req[0] != 5 {
		return "", fmt.Errorf("bad request version")
	}
	if req[1] != 1 { // только CONNECT
		conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0}) // command not supported
		return "", errUnsupportedCmd{cmd: req[1]}
	}

	// 3. Читаем адрес
	var host string
	switch req[3] {
	case 1: // IPv4
		addr := make([]byte, 4)
		io.ReadFull(conn, addr)
		host = net.IP(addr).String()

	case 3: // domain name
		lenBuf := make([]byte, 1)
		io.ReadFull(conn, lenBuf)
		domain := make([]byte, lenBuf[0])
		io.ReadFull(conn, domain)
		host = string(domain)

	case 4: // IPv6
		addr := make([]byte, 16)
		io.ReadFull(conn, addr)
		host = "[" + net.IP(addr).String() + "]"

	default:
		return "", fmt.Errorf("unknown address type: %d", req[3])
	}

	// 4. Читаем порт
	portBuf := make([]byte, 2)
	io.ReadFull(conn, portBuf)
	port := binary.BigEndian.Uint16(portBuf)

	target := fmt.Sprintf("%s:%d", host, port)

	// 5. Отвечаем "success"
	conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})

	return target, nil
}
