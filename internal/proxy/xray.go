package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"text/template"
	"bytes"
	"time"
)

// XrayProcess управляет subprocess xray-core.
type XrayProcess struct {
	cmd        *exec.Cmd
	configPath string
}

// XrayConfig параметры для генерации xray конфига из шаблона.
type XrayConfig struct {
	VDSHost    string
	VDSPort    int
	UUID       string
	SNI        string
	PublicKey  string
	ShortID    string
	Socks5Port int
}

// StartXray запускает xray как дочерний процесс.
// Генерирует конфиг из шаблона, затем запускает xray.
func StartXray(templatePath, configPath string, cfg XrayConfig) (*XrayProcess, error) {
	// Генерируем конфиг из шаблона
	if err := generateXrayConfig(templatePath, configPath, cfg); err != nil {
		return nil, fmt.Errorf("генерация xray конфига: %w", err)
	}

	// Запускаем xray
	cmd := exec.Command("xray", "-config", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("не удалось запустить xray: %w", err)
	}

	p := &XrayProcess{cmd: cmd, configPath: configPath}

	// Небольшая пауза чтобы xray успел стартовать
	time.Sleep(500 * time.Millisecond)

	return p, nil
}

// Stop останавливает xray процесс.
func (p *XrayProcess) Stop() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("не удалось остановить xray (PID %d): %w", p.cmd.Process.Pid, err)
	}
	_ = p.cmd.Wait()
	// Удаляем временный конфиг
	_ = os.Remove(p.configPath)
	return nil
}

// IsRunning проверяет что процесс xray жив.
func (p *XrayProcess) IsRunning() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	// Сигнал 0 проверяет существование процесса без его остановки
	err := p.cmd.Process.Signal(os.Signal(nil))
	// nil означает что процесс ещё жив
	_ = err
	// Проверяем через /proc
	pidPath := fmt.Sprintf("/proc/%d", p.cmd.Process.Pid)
	_, err = os.Stat(pidPath)
	return err == nil
}

// PID возвращает PID процесса xray.
func (p *XrayProcess) PID() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// generateXrayConfig генерирует xray конфиг из шаблона.
func generateXrayConfig(templatePath, outputPath string, cfg XrayConfig) error {
	// Читаем шаблон
	tmplData, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("не удалось прочитать шаблон %s: %w", templatePath, err)
	}

	// Парсим и выполняем шаблон
	tmpl, err := template.New("xray").Parse(string(tmplData))
	if err != nil {
		return fmt.Errorf("ошибка парсинга шаблона: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return fmt.Errorf("ошибка генерации конфига: %w", err)
	}

	// Сохраняем результат (права 0600 — только root)
	if err := os.WriteFile(outputPath, buf.Bytes(), 0600); err != nil {
		return fmt.Errorf("не удалось записать конфиг %s: %w", outputPath, err)
	}

	return nil
}
