package swarmnode

// tproxy.go — прозрачный прокси (TPROXY).
//
// Позволяет swarm-node работать как шлюз для всех домашних устройств
// БЕЗ xray — напрямую перехватывает форвардируемый трафик.
//
// Схема:
//   Домашние устройства → роутер (шлюз = IP коробки)
//       → iptables TPROXY → swarm-node:12346
//       → swarm-node разбирает оригинальный dst
//       → маршрутизирует через рой
//
// Аналог xray-tproxy, но нативный для swarm-node.

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"syscall"
	"unsafe"
)

// tproxyServer принимает трафик через TPROXY и проксирует через рой.
type tproxyServer struct {
	node *Node
}

// Listen запускает TPROXY listener на указанном адресе.
// Требует root и iptables правил (см. iptables-tproxy.sh).
func (t *tproxyServer) Listen(ctx context.Context, addr string) error {
	ln, err := listenTProxy(addr)
	if err != nil {
		return fmt.Errorf("tproxy listen %s: %w", addr, err)
	}
	defer ln.Close()

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	log.Printf("[tproxy] Слушаем на %s", addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.Printf("[tproxy] accept: %v", err)
				continue
			}
		}
		go t.handleConn(ctx, conn)
	}
}

// handleConn обрабатывает одно TPROXY-соединение.
// Оригинальный destination определяется из SO_ORIGINAL_DST.
func (t *tproxyServer) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// Получаем оригинальный адрес назначения
	target, err := getOriginalDst(conn)
	if err != nil {
		log.Printf("[tproxy] original dst: %v", err)
		return
	}

	// Выбираем пир для маршрутизации
	peer := t.node.selectPeer()
	if peer == nil {
		// Fallback: прямое подключение
		t.directConnect(conn, target)
		return
	}

	// Открываем поток к пиру
	stream, err := peer.OpenProxyStream(ctx, target)
	if err != nil {
		log.Printf("[tproxy] open stream to %s: %v", target, err)
		// Fallback
		t.directConnect(conn, target)
		return
	}
	defer stream.Close()

	log.Printf("[tproxy] %s → [%s] → %s", conn.RemoteAddr(), peer.NodeIDShort(), target)

	// Двунаправленная передача с подсчётом байт (как в socks5.go)
	done := make(chan struct{}, 2)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			nr, err := conn.Read(buf)
			if nr > 0 {
				stream.Write(buf[:nr])
				t.node.addProxiedBytes(int64(nr), 0)
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			nr, err := stream.Read(buf)
			if nr > 0 {
				conn.Write(buf[:nr])
				t.node.addProxiedBytes(0, int64(nr))
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	<-done
}

// directConnect — прямое подключение без роя (fallback).
func (t *tproxyServer) directConnect(conn net.Conn, target string) {
	remote, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("[tproxy] direct dial %s: %v", target, err)
		return
	}
	defer remote.Close()

	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, remote); done <- struct{}{} }()
	<-done
}

// ─── Системный вызов ──────────────────────────────────────────────────────

// listenTProxy создаёт TCP listener с SO_TRANSPARENT (нужно для TPROXY).
func listenTProxy(addr string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var setSockOptErr error
			err := c.Control(func(fd uintptr) {
				// SO_TRANSPARENT позволяет принимать соединения с чужими dst адресами
				setSockOptErr = syscall.SetsockoptInt(int(fd), syscall.SOL_IP,
					syscall.IP_TRANSPARENT, 1)
			})
			if err != nil {
				return err
			}
			return setSockOptErr
		},
	}
	return lc.Listen(context.Background(), "tcp", addr)
}

// getOriginalDst возвращает оригинальный адрес назначения из TPROXY-соединения.
// Использует SO_ORIGINAL_DST через getsockopt.
func getOriginalDst(conn net.Conn) (string, error) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return "", fmt.Errorf("not a TCP connection")
	}

	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return "", fmt.Errorf("syscall conn: %w", err)
	}

	var origDst syscall.RawSockaddrInet4
	var innerErr error

	err = rawConn.Control(func(fd uintptr) {
		// SO_ORIGINAL_DST = 80 (Linux)
		const SO_ORIGINAL_DST = 80
		size := uint32(unsafe.Sizeof(origDst))
		_, _, errno := syscall.Syscall6(
			syscall.SYS_GETSOCKOPT,
			fd,
			syscall.IPPROTO_IP,
			SO_ORIGINAL_DST,
			uintptr(unsafe.Pointer(&origDst)),
			uintptr(unsafe.Pointer(&size)),
			0,
		)
		if errno != 0 {
			innerErr = errno
		}
	})
	if err != nil {
		return "", err
	}
	if innerErr != nil {
		return "", fmt.Errorf("getsockopt SO_ORIGINAL_DST: %w", innerErr)
	}

	// Конвертируем в "host:port"
	ip := net.IP(origDst.Addr[:])
	port := (uint16(origDst.Port)>>8 | uint16(origDst.Port)<<8) // big-endian
	return fmt.Sprintf("%s:%d", ip.String(), port), nil
}

// ─── Запуск tproxy ────────────────────────────────────────────────────────

// startTProxy запускает встроенный TPROXY сервер.
func (n *Node) startTProxy(addr string) {
	t := &tproxyServer{node: n}
	if err := t.Listen(n.ctx, addr); err != nil {
		log.Printf("[node] TPROXY ошибка: %v", err)
	}
}
