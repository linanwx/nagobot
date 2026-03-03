package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

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
	if err := mgr.Install(installPath, logDir); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("==> Installation complete!")
	fmt.Printf("    Binary:  %s\n", installPath)
	fmt.Printf("    Logs:    %s/\n", logDir)
	fmt.Println()

	// Check if install dir is in PATH.
	if !isInPath(installDir) {
		fmt.Printf("    NOTE: Add %s to your PATH:\n", installDir)
		fmt.Printf("      export PATH=\"%s:$PATH\"\n", installDir)
		fmt.Println()
	}

	fmt.Println("==> Next steps:")
	fmt.Println("    1. nagobot cli")
	fmt.Println("    2. /init --provider openrouter --api-key YOUR_KEY --model moonshotai/kimi-k2.5")
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
