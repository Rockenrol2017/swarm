package proxy

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"
)

// Tun2SocksProcess управляет subprocess tun2socks.
//
// ВАЖНО: tun2socks используется как SUBPROCESS, не как библиотека.
// Причина: внутренний API tun2socks нестабилен и меняется между версиями.
// Subprocess изолирует нас от изменений upstream.
type Tun2SocksProcess struct {
	cmd *exec.Cmd
}

// StartTun2Socks запускает tun2socks как дочерний процесс.
//
// tunName   — имя TUN интерфейса (например "swarm0")
// socks5Addr — адрес SOCKS5 прокси (например "127.0.0.1:1080")
func StartTun2Socks(tunName, socks5Addr string) (*Tun2SocksProcess, error) {
	// Команда: tun2socks -device swarm0 -proxy socks5://127.0.0.1:1080
	cmd := exec.Command(
		"tun2socks",
		"-device", tunName,
		"-proxy", "socks5://"+socks5Addr,
		"-loglevel", "warning",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("не удалось запустить tun2socks: %w", err)
	}

	p := &Tun2SocksProcess{cmd: cmd}

	// Небольшая пауза для инициализации
	time.Sleep(200 * time.Millisecond)

	return p, nil
}

// Stop останавливает tun2socks процесс.
func (p *Tun2SocksProcess) Stop() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("не удалось остановить tun2socks (PID %d): %w",
			p.cmd.Process.Pid, err)
	}
	_ = p.cmd.Wait()
	return nil
}

// IsRunning проверяет что процесс tun2socks жив.
func (p *Tun2SocksProcess) IsRunning() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	pidPath := fmt.Sprintf("/proc/%d", p.cmd.Process.Pid)
	_, err := os.Stat(pidPath)
	return err == nil
}

// WaitForSocks5 ожидает пока SOCKS5 прокси станет доступен.
// timeout — максимальное время ожидания.
// Возвращает ошибку если прокси не поднялся за timeout.
func WaitForSocks5(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("SOCKS5 прокси %s не стартовал за %v", addr, timeout)
}
