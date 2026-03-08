package service

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

// New returns a Windows service manager.
func New() Manager { return &windowsManager{} }

const regKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
const regValue = "nagobot"

type windowsManager struct{}

func (m *windowsManager) Install(binPath, logDir string) error {
	return m.install(binPath)
}

func (m *windowsManager) InstallElevated(binPath, logDir string) error {
	// No elevation needed — Registry Run key is per-user.
	return m.install(binPath)
}

func (m *windowsManager) install(binPath string) error {
	// Register for auto-start on logon via Registry Run key (no admin required).
	regData := fmt.Sprintf(`"%s" serve`, binPath)
	out, err := exec.Command("reg", "add", regKey,
		"/v", regValue, "/t", "REG_SZ", "/d", regData, "/f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("reg add failed: %s (%w)", string(out), err)
	}

	// Start serve in background immediately.
	serveCmd := exec.Command(binPath, "serve")
	serveCmd.Stdout = nil
	serveCmd.Stderr = nil
	serveCmd.Stdin = nil
	if err := serveCmd.Start(); err != nil {
		fmt.Printf("    Warning: could not start serve: %v\n", err)
	}

	fmt.Println("    Service: Registry Run key (auto-start on logon)")
	return nil
}

func (m *windowsManager) Restart() error {
	binPath := filepath.Join(DefaultInstallDir(), DefaultBinName())
	serveCmd := exec.Command(binPath, "serve")
	serveCmd.Stdout = nil
	serveCmd.Stderr = nil
	serveCmd.Stdin = nil
	if err := serveCmd.Start(); err != nil {
		return fmt.Errorf("failed to start serve: %w", err)
	}
	return nil
}

func (m *windowsManager) Uninstall() error {
	fmt.Println("==> Removing auto-start entry...")
	_ = exec.Command("reg", "delete", regKey, "/v", regValue, "/f").Run()
	return nil
}
