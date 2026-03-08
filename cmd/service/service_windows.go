package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// New returns a Windows service manager.
func New() Manager { return &windowsManager{} }

const taskName = "nagobot"

type windowsManager struct{}

func (m *windowsManager) Install(binPath, logDir string) error {
	return m.install(binPath, logDir)
}

func (m *windowsManager) InstallElevated(binPath, logDir string) error {
	// Write elevated process output to a temp file so we can report errors.
	tmpDir := os.TempDir()
	outFile := filepath.Join(tmpDir, "nagobot-install.log")

	// Use -RedirectStandardOutput/-RedirectStandardError + -PassThru to capture exit code.
	// Single-quote paths in ArgumentList to avoid Go %q double-backslash issues.
	psCmd := fmt.Sprintf(
		`$p = Start-Process -FilePath '%s' -Verb RunAs -Wait -PassThru `+
			`-RedirectStandardOutput '%s' `+
			`-RedirectStandardError '%s' `+
			`-ArgumentList 'service-install','--bin-path','%s','--log-dir','%s'; `+
			`exit $p.ExitCode`,
		binPath, outFile, outFile+".err", binPath, logDir,
	)
	_, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd).CombinedOutput()
	if err != nil {
		// Read the elevated process output for diagnostics.
		var detail string
		if b, e := os.ReadFile(outFile); e == nil {
			detail += strings.TrimSpace(string(b))
		}
		if b, e := os.ReadFile(outFile + ".err"); e == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				detail += "\n" + s
			}
		}
		if detail != "" {
			return fmt.Errorf("elevated service install failed: %s (%w)", detail, err)
		}
		return fmt.Errorf("elevated service install failed: %w", err)
	}
	// Clean up temp files.
	os.Remove(outFile)
	os.Remove(outFile + ".err")
	return nil
}

func (m *windowsManager) install(binPath, logDir string) error {
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

func (m *windowsManager) Restart() error {
	_ = exec.Command("schtasks", "/end", "/tn", taskName).Run()
	if out, err := exec.Command("schtasks", "/run", "/tn", taskName).CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks restart failed: %s (%w)", string(out), err)
	}
	return nil
}

func (m *windowsManager) Uninstall() error {
	fmt.Println("==> Stopping scheduled task...")
	_ = exec.Command("schtasks", "/end", "/tn", taskName).Run()

	fmt.Println("==> Removing scheduled task...")
	_ = exec.Command("schtasks", "/delete", "/tn", taskName, "/f").Run()

	return nil
}
