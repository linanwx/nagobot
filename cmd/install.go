package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/linanwx/nagobot/cmd/service"
	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install nagobot as a system service",
	Long: `Install nagobot as a system service that starts automatically on login.

Supported platforms:
  - macOS: launchd (~/Library/LaunchAgents)
  - Linux: systemd user service (~/.config/systemd/user)
  - Windows: Task Scheduler

The command copies the current binary to ~/.local/bin and registers
a service that runs "nagobot serve" in the background.

Run "nagobot onboard" before or after install to configure your bot.`,
	RunE: runInstall,
}

func init() {
	rootCmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) error {
	installDir := service.DefaultInstallDir()
	binName := service.DefaultBinName()
	installPath := filepath.Join(installDir, binName)

	// Get current binary path.
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine current binary path: %w", err)
	}
	selfPath, err = filepath.EvalSymlinks(selfPath)
	if err != nil {
		return fmt.Errorf("cannot resolve binary path: %w", err)
	}

	// Install binary.
	fmt.Printf("==> Installing binary to %s...\n", installPath)
	if err := installBinaryTo(selfPath, installDir, installPath); err != nil {
		return fmt.Errorf("cannot install binary: %w", err)
	}

	// Create log directory.
	configDir, err := config.ConfigDir()
	if err != nil {
		return fmt.Errorf("cannot determine config directory: %w", err)
	}
	logDir := filepath.Join(configDir, "logs")
	fmt.Println("==> Creating log directory...")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("cannot create log directory: %w", err)
	}

	// Register as system service.
	fmt.Println("==> Registering system service...")
	mgr := service.New()
	if err := mgr.InstallElevated(installPath, logDir); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("==> Installation complete!")
	fmt.Printf("    Binary:  %s\n", installPath)
	fmt.Printf("    Logs:    %s/\n", logDir)
	fmt.Println()

	// Ensure install dir is in PATH (platform-specific).
	ensurePath(installDir)

	fmt.Println("==> Next steps:")
	fmt.Println("    1. nagobot onboard")
	fmt.Println("    2. nagobot cli")
	return nil
}

func isInPath(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == dir {
			return true
		}
	}
	return false
}

func ensurePath(dir string) {
	if isInPath(dir) {
		return
	}
	switch runtime.GOOS {
	case "windows":
		ensurePathWindows(dir)
	default:
		ensurePathUnix(dir)
	}
}

func ensurePathWindows(dir string) {
	// Read current user PATH from registry.
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`[Environment]::GetEnvironmentVariable("Path", "User")`).Output()
	if err != nil {
		fmt.Printf("    NOTE: Add %s to your PATH, then restart the terminal.\n", dir)
		return
	}
	userPath := strings.TrimSpace(string(out))
	if strings.Contains(strings.ToLower(userPath), strings.ToLower(dir)) {
		fmt.Printf("    PATH: %s is already in PATH. Restart your terminal to take effect.\n", dir)
		return
	}
	// Add to user PATH.
	newPath := dir + ";" + userPath
	psSet := fmt.Sprintf(`[Environment]::SetEnvironmentVariable("Path", '%s', "User")`, newPath)
	if err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psSet).Run(); err != nil {
		fmt.Printf("    NOTE: Add %s to your PATH, then restart the terminal.\n", dir)
		return
	}
	fmt.Printf("    PATH: added %s (restart your terminal to take effect)\n", dir)
}

func ensurePathUnix(dir string) {
	rc := detectRCFile()
	if rc == "" {
		fmt.Printf("    NOTE: Add to your PATH:\n")
		fmt.Printf("      export PATH=\"%s:$PATH\"\n", dir)
		return
	}
	line := fmt.Sprintf(`export PATH="%s:$PATH"`, dir)
	// Check if already written to RC file.
	content, err := os.ReadFile(rc)
	if err == nil && strings.Contains(string(content), dir) {
		fmt.Printf("    PATH: already in %s. Run to take effect:\n", rc)
		fmt.Printf("      source %s\n", rc)
		return
	}
	// Write to RC file.
	f, err := os.OpenFile(rc, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		fmt.Printf("    NOTE: Add to your PATH:\n")
		fmt.Printf("      %s\n", line)
		return
	}
	fmt.Fprintf(f, "\n# nagobot\n%s\n", line)
	f.Close()
	fmt.Printf("    PATH: added to %s. Run to take effect:\n", rc)
	fmt.Printf("      source %s\n", rc)
}

func detectRCFile() string {
	home, _ := os.UserHomeDir()
	shell := os.Getenv("SHELL")
	if strings.HasSuffix(shell, "/zsh") {
		return filepath.Join(home, ".zshrc")
	}
	bashrc := filepath.Join(home, ".bashrc")
	if _, err := os.Stat(bashrc); err == nil {
		return bashrc
	}
	profile := filepath.Join(home, ".profile")
	if _, err := os.Stat(profile); err == nil {
		return profile
	}
	return ""
}

func installBinaryTo(src, dir, dst string) error {
	srcAbs, _ := filepath.Abs(src)
	dstAbs, _ := filepath.Abs(dst)
	if srcAbs == dstAbs {
		return nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("cannot create directory %s: %w", dir, err)
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Chmod(dst, 0755)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
