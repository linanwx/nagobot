package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/linanwx/nagobot/cmd/service"
	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove nagobot system service and binary",
	Long: `Remove the nagobot system service registration and installed binary.

This stops the running service and removes the service configuration.
The data directory (~/.nagobot) is preserved — remove it manually if desired.`,
	RunE: runUninstall,
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}

func runUninstall(cmd *cobra.Command, args []string) error {
	// Unregister system service.
	mgr := service.New()
	if err := mgr.Uninstall(); err != nil {
		return err
	}

	// Remove installed binary.
	installPath := filepath.Join(service.DefaultInstallDir(), service.DefaultBinName())

	fmt.Println("==> Removing binary...")
	if err := os.Remove(installPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "    Warning: could not remove %s: %v\n", installPath, err)
	}

	// Remove socket file.
	socketPath, err := config.SocketPath()
	if err == nil {
		fmt.Println("==> Removing socket...")
		os.Remove(socketPath)
	}

	fmt.Println()
	fmt.Println("==> Uninstall complete.")
	configDir, _ := config.ConfigDir()
	if configDir != "" {
		fmt.Printf("    Data directory preserved: %s\n", configDir)
	}
	fmt.Println("    To remove all data: rm -rf ~/.nagobot")
	return nil
}
