package tun

import (
	"fmt"
	"os/exec"
)

// Device представляет TUN интерфейс swarm0.
// Создаётся через ip команды (не требует CGO).
type Device struct {
	Name string
	IP   string
}

// New создаёт новый TUN интерфейс.
// name — имя интерфейса (например "swarm0")
// ip   — IP адрес (например "10.0.0.1")
func New(name, ip string) (*Device, error) {
	d := &Device{Name: name, IP: ip}

	// Создать TUN интерфейс через ip tuntap
	if err := run("ip", "tuntap", "add", "dev", name, "mode", "tun"); err != nil {
		return nil, fmt.Errorf("не удалось создать TUN %s: %w", name, err)
	}

	// Назначить IP адрес
	cidr := ip + "/24"
	if err := run("ip", "addr", "add", cidr, "dev", name); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("не удалось назначить IP %s → %s: %w", cidr, name, err)
	}

	// Поднять интерфейс
	if err := run("ip", "link", "set", "dev", name, "up"); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("не удалось поднять интерфейс %s: %w", name, err)
	}

	return d, nil
}

// Close удаляет TUN интерфейс.
func (d *Device) Close() error {
	if err := run("ip", "link", "del", d.Name); err != nil {
		return fmt.Errorf("не удалось удалить TUN %s: %w", d.Name, err)
	}
	return nil
}

// SetMTU устанавливает MTU для интерфейса.
// Рекомендуется 1420 для VLESS/Reality (учитывает overhead заголовков).
func (d *Device) SetMTU(mtu int) error {
	if err := run("ip", "link", "set", "dev", d.Name, "mtu", fmt.Sprint(mtu)); err != nil {
		return fmt.Errorf("не удалось установить MTU %d для %s: %w", mtu, d.Name, err)
	}
	return nil
}

// run выполняет системную команду.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w\n%s", name, args, err, string(out))
	}
	return nil
}
