package cmd

import (
	"fmt"

	"github.com/linanwx/nagobot/cmd/service"
	"github.com/spf13/cobra"
)

var (
	serviceInstallBinPath string
	serviceInstallLogDir  string
)

var serviceInstallCmd = &cobra.Command{
	Use:    "service-install",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if serviceInstallBinPath == "" || serviceInstallLogDir == "" {
			return fmt.Errorf("--bin-path and --log-dir are required")
		}
		return service.New().Install(serviceInstallBinPath, serviceInstallLogDir)
	},
}

func init() {
	serviceInstallCmd.Flags().StringVar(&serviceInstallBinPath, "bin-path", "", "Installed nagobot binary path")
	serviceInstallCmd.Flags().StringVar(&serviceInstallLogDir, "log-dir", "", "Log directory")
	rootCmd.AddCommand(serviceInstallCmd)
}
