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
Environment=HOME={{.Home}} PATH={{.Home}}/.local/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin

[Install]
WantedBy={{.WantedBy}}
`))

type unitData struct {
	BinPath  string
	Home     string
	WantedBy string
}

type linuxManager struct{}

// isRoot returns true when running as UID 0.
func isRoot() bool { return os.Getuid() == 0 }

func (m *linuxManager) InstallElevated(binPath, logDir string) error {
	return m.Install(binPath, logDir)
}

func (m *linuxManager) Install(binPath, logDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	var unitPath string
	if isRoot() {
		// Root: system-level unit — no D-Bus user session required.
		unitPath = filepath.Join("/etc/systemd/system", unitName)
	} else {
		unitDir := filepath.Join(home, ".config", "systemd", "user")
		if err := os.MkdirAll(unitDir, 0755); err != nil {
			return fmt.Errorf("cannot create systemd user directory: %w", err)
		}
		unitPath = filepath.Join(unitDir, unitName)
	}

	// Stop existing service if any.
	_ = systemctl(isRoot(), "stop", "nagobot")

	// Write unit file.
	f, err := os.Create(unitPath)
	if err != nil {
		return fmt.Errorf("cannot create unit file at %s: %w", unitPath, err)
	}
	defer f.Close()

	wantedBy := "default.target"
	if isRoot() {
		wantedBy = "multi-user.target"
	}

	if err := unitTemplate.Execute(f, unitData{
		BinPath:  binPath,
		Home:     home,
		WantedBy: wantedBy,
	}); err != nil {
		return fmt.Errorf("cannot write unit file: %w", err)
	}

	// Reload and enable.
	if out, err := systemctlOutput(isRoot(), "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %s (%w)", string(out), err)
	}
	if out, err := systemctlOutput(isRoot(), "enable", "--now", "nagobot"); err != nil {
		return fmt.Errorf("systemctl enable failed: %s (%w)", string(out), err)
	}

	// Enable linger for non-root user so the service survives SSH disconnects.
	if !isRoot() {
		_ = exec.Command("loginctl", "enable-linger").Run()
	}

	if isRoot() {
		fmt.Println("    Service: systemctl status nagobot")
	} else {
		fmt.Println("    Service: systemctl --user status nagobot")
	}
	return nil
}

func (m *linuxManager) Restart() error {
	if out, err := systemctlOutput(isRoot(), "restart", "nagobot"); err != nil {
		return fmt.Errorf("systemctl restart failed: %s (%w)", string(out), err)
	}
	return nil
}

func (m *linuxManager) Uninstall() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	var unitPath string
	if isRoot() {
		unitPath = filepath.Join("/etc/systemd/system", unitName)
	} else {
		unitPath = filepath.Join(home, ".config", "systemd", "user", unitName)
	}

	fmt.Println("==> Stopping service...")
	_ = systemctl(isRoot(), "disable", "--now", "nagobot")

	fmt.Println("==> Removing systemd unit...")
	os.Remove(unitPath)

	_ = systemctl(isRoot(), "daemon-reload")

	return nil
}

// systemctl runs systemctl with or without --user depending on root status.
func systemctl(root bool, args ...string) error {
	if !root {
		args = append([]string{"--user"}, args...)
	}
	return exec.Command("systemctl", args...).Run()
}

// systemctlOutput is like systemctl but returns combined output.
func systemctlOutput(root bool, args ...string) ([]byte, error) {
	if !root {
		args = append([]string{"--user"}, args...)
	}
	return exec.Command("systemctl", args...).CombinedOutput()
}
