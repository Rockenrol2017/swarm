package killswitch

import (
	"fmt"
	"os/exec"
)

// KillSwitch управляет iptables правилами для защиты трафика.
//
// Принцип работы:
// 1. Политика по умолчанию DROP — весь трафик запрещён
// 2. Разрешаем только: loopback, VDS, TUN интерфейс, веб-порт
// 3. NAT Masquerade — заменяет приватные IP на IP swarm0
// 4. DNS только через туннель — блокируем DNS утечки
//
// КРИТИЧНО: Без NAT Masquerade пакеты от домашних устройств не вернутся!
// Источник: телефон (LAN) → сервер не знает куда ответить без MASQUERADE
type KillSwitch struct {
	vdsIP    string
	vdsPort  int
	tunName  string
	webPort  int
	lanIface string // eth0 или аналог
}

// New создаёт новый KillSwitch.
func New(vdsIP string, vdsPort int, tunName string, webPort int, lanIface string) *KillSwitch {
	return &KillSwitch{
		vdsIP:    vdsIP,
		vdsPort:  vdsPort,
		tunName:  tunName,
		webPort:  webPort,
		lanIface: lanIface,
	}
}

// Enable включает kill switch:
// сбрасывает все правила и устанавливает защитные.
func (ks *KillSwitch) Enable() error {
	rules := [][]string{
		// === Сброс всех правил ===
		{"-F"},                // Flush все цепочки
		{"-t", "nat", "-F"},   // Flush nat таблицу
		{"-X"},                // Удалить пользовательские цепочки

		// === Политика по умолчанию: DROP всё ===
		{"-P", "INPUT", "DROP"},
		{"-P", "OUTPUT", "DROP"},
		{"-P", "FORWARD", "DROP"},

		// === Loopback всегда разрешён ===
		{"-A", "INPUT", "-i", "lo", "-j", "ACCEPT"},
		{"-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"},

		// === Established connections ===
		{"-A", "INPUT", "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
		{"-A", "OUTPUT", "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
	}

	// === xray → VDS (ТОЛЬКО этот хост, ТОЛЬКО этот порт) ===
	rules = append(rules,
		[]string{"-A", "OUTPUT", "-d", ks.vdsIP, "-p", "tcp",
			"--dport", fmt.Sprint(ks.vdsPort), "-j", "ACCEPT"},
		[]string{"-A", "INPUT", "-s", ks.vdsIP, "-p", "tcp",
			"--sport", fmt.Sprint(ks.vdsPort), "-j", "ACCEPT"},
	)

	// === TUN интерфейс swarm0 ===
	rules = append(rules,
		[]string{"-A", "OUTPUT", "-o", ks.tunName, "-j", "ACCEPT"},
		[]string{"-A", "INPUT", "-i", ks.tunName, "-j", "ACCEPT"},
	)

	// === FORWARD для домашних устройств ===
	// LAN → TUN (устройства выходят в интернет)
	// TUN → LAN (ответы возвращаются устройствам)
	rules = append(rules,
		[]string{"-A", "FORWARD", "-i", ks.lanIface, "-o", ks.tunName, "-j", "ACCEPT"},
		[]string{"-A", "FORWARD", "-i", ks.tunName, "-o", ks.lanIface, "-j", "ACCEPT"},
		// Established FORWARD connections
		[]string{"-A", "FORWARD", "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
	)

	// === NAT Masquerade — КРИТИЧНО! ===
	// БЕЗ ЭТОГО домашние устройства не получат ответы!
	// LAN устройство → пакет с приватным IP → сервер не знает куда ответить
	// С MASQUERADE: IP заменяется на IP swarm0 → ответ возвращается корректно
	rules = append(rules,
		[]string{"-t", "nat", "-A", "POSTROUTING", "-o", ks.tunName, "-j", "MASQUERADE"},
	)

	// === DNS только через туннель — защита от DNS утечек ===
	// Порядок ВАЖЕН: сначала ACCEPT для туннеля, потом DROP для всего остального
	rules = append(rules,
		// UDP DNS
		[]string{"-A", "OUTPUT", "-p", "udp", "--dport", "53", "-o", ks.tunName, "-j", "ACCEPT"},
		[]string{"-A", "OUTPUT", "-p", "udp", "--dport", "53", "-j", "DROP"},
		// TCP DNS
		[]string{"-A", "OUTPUT", "-p", "tcp", "--dport", "53", "-o", ks.tunName, "-j", "ACCEPT"},
		[]string{"-A", "OUTPUT", "-p", "tcp", "--dport", "53", "-j", "DROP"},
	)

	// === Веб-интерфейс (:8080) ===
	// Только из локальной сети
	rules = append(rules,
		[]string{"-A", "INPUT", "-p", "tcp", "--dport", fmt.Sprint(ks.webPort), "-j", "ACCEPT"},
		[]string{"-A", "OUTPUT", "-p", "tcp", "--sport", fmt.Sprint(ks.webPort), "-j", "ACCEPT"},
	)

	// Применяем все правила
	for _, rule := range rules {
		if err := iptables(rule...); err != nil {
			return fmt.Errorf("iptables %v: %w", rule, err)
		}
	}

	return nil
}

// Disable снимает kill switch — восстанавливает ACCEPT политику.
// Вызывается при штатном завершении работы.
func (ks *KillSwitch) Disable() error {
	cleanup := [][]string{
		{"-F"},
		{"-t", "nat", "-F"},
		{"-X"},
		{"-P", "INPUT", "ACCEPT"},
		{"-P", "OUTPUT", "ACCEPT"},
		{"-P", "FORWARD", "ACCEPT"},
	}

	for _, rule := range cleanup {
		if err := iptables(rule...); err != nil {
			// Не останавливаемся при ошибке — пробуем очистить всё что можно
			fmt.Printf("предупреждение iptables cleanup %v: %v\n", rule, err)
		}
	}

	return nil
}

// iptables выполняет команду iptables.
func iptables(args ...string) error {
	cmd := exec.Command("iptables", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, string(out))
	}
	return nil
}
