package service

import (
	"fmt"
	"os/exec"
)

// New returns a Windows service manager.
func New() Manager { return &windowsManager{} }

const taskName = "nagobot"

type windowsManager struct{}

func (m *windowsManager) Install(binPath, logDir string) error {
	// Remove existing task if any.
	_ = exec.Command("schtasks", "/delete", "/tn", taskName, "/f").Run()

	// Create a task that runs at user logon and restarts on failure.
	out, err := exec.Command("schtasks", "/create",
		"/tn", taskName,
		"/tr", fmt.Sprintf(`"%s" serve`, binPath),
		"/sc", "onlogon",
		"/rl", "limited",
		"/f",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks create failed: %s (%w)", string(out), err)
	}

	// Start the task immediately.
	if out, err := exec.Command("schtasks", "/run", "/tn", taskName).CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks run failed: %s (%w)", string(out), err)
	}

	fmt.Println("    Service: schtasks /query /tn " + taskName)
	return nil
}

func (m *windowsManager) Uninstall() error {
	fmt.Println("==> Stopping scheduled task...")
	_ = exec.Command("schtasks", "/end", "/tn", taskName).Run()

	fmt.Println("==> Removing scheduled task...")
	_ = exec.Command("schtasks", "/delete", "/tn", taskName, "/f").Run()

	return nil
}
