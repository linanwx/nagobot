package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

// New returns a Linux service manager.
func New() Manager { return &linuxManager{} }

const unitName = "nagobot.service"

var unitTemplate = template.Must(template.New("unit").Parse(`[Unit]
Description=nagobot AI assistant
After=network-online.target

[Service]
ExecStart={{.BinPath}} serve
Restart=on-failure
RestartSec=5
Environment=HOME={{.Home}}

[Install]
WantedBy=default.target
`))

type unitData struct {
	BinPath string
	Home    string
}

type linuxManager struct{}

func (m *linuxManager) InstallElevated(binPath, logDir string) error {
	return m.Install(binPath, logDir)
}

func (m *linuxManager) Install(binPath, logDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		return fmt.Errorf("cannot create systemd user directory: %w", err)
	}

	unitPath := filepath.Join(unitDir, unitName)

	// Stop existing service if any.
	_ = exec.Command("systemctl", "--user", "stop", "nagobot").Run()

	// Write unit file.
	f, err := os.Create(unitPath)
	if err != nil {
		return fmt.Errorf("cannot create unit file at %s: %w", unitPath, err)
	}
	defer f.Close()

	if err := unitTemplate.Execute(f, unitData{
		BinPath: binPath,
		Home:    home,
	}); err != nil {
		return fmt.Errorf("cannot write unit file: %w", err)
	}

	// Reload and enable.
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %s (%w)", string(out), err)
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", "nagobot").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable failed: %s (%w)", string(out), err)
	}

	fmt.Println("    Service: systemctl --user status nagobot")
	return nil
}

func (m *linuxManager) Restart() error {
	if out, err := exec.Command("systemctl", "--user", "restart", "nagobot").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl restart failed: %s (%w)", string(out), err)
	}
	return nil
}

func (m *linuxManager) Uninstall() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	unitPath := filepath.Join(home, ".config", "systemd", "user", unitName)

	fmt.Println("==> Stopping service...")
	_ = exec.Command("systemctl", "--user", "disable", "--now", "nagobot").Run()

	fmt.Println("==> Removing systemd unit...")
	os.Remove(unitPath)

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()

	return nil
}
