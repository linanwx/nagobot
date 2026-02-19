package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/config"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Non-interactive setup: generate config and workspace files",
	Long: `Generate config.yaml and bootstrap workspace files without interactive prompts.
Existing files are never overwritten.

Examples:
  nagobot init --provider deepseek --model deepseek-reasoner --api-key sk-xxx
  nagobot init --provider anthropic --model claude-sonnet-4-6 --api-key sk-xxx --telegram-token BOT_TOKEN`,
	RunE: runInit,
}

var (
	initProvider      string
	initModel         string
	initAPIKey        string
	initTelegramToken string
	initAdminUserID   string
)

func init() {
	initCmd.Flags().StringVar(&initProvider, "provider", "deepseek", "LLM provider name")
	initCmd.Flags().StringVar(&initModel, "model", "", "Model type (defaults to provider's first supported model)")
	initCmd.Flags().StringVar(&initAPIKey, "api-key", "", "Provider API key (required)")
	initCmd.Flags().StringVar(&initTelegramToken, "telegram-token", "", "Telegram bot token (optional)")
	initCmd.Flags().StringVar(&initAdminUserID, "admin-user-id", "", "Admin user ID for Telegram (optional)")
	rootCmd.AddCommand(initCmd)
}

func runInit(_ *cobra.Command, _ []string) error {
	apiKey := strings.TrimSpace(initAPIKey)
	if apiKey == "" {
		return fmt.Errorf("--api-key is required")
	}

	cfg := config.DefaultConfig()
	cfg.SetProvider(initProvider)
	if initModel != "" {
		cfg.SetModelType(initModel)
	}
	cfg.SetProviderAPIKey(apiKey)

	if strings.TrimSpace(initTelegramToken) != "" {
		cfg.Channels.Telegram.Token = strings.TrimSpace(initTelegramToken)
	}
	if strings.TrimSpace(initAdminUserID) != "" {
		cfg.Channels.AdminUserID = strings.TrimSpace(initAdminUserID)
	}

	// Save config only if it does not exist.
	configPath, err := config.ConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(configPath); err == nil {
		fmt.Println("Config already exists, skipping:", configPath)
	} else {
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Println("Config created:", configPath)
	}

	// Bootstrap workspace files (skip existing).
	if err := cfg.EnsureWorkspace(); err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return err
	}
	if err := createBootstrapFiles(workspace); err != nil {
		return fmt.Errorf("failed to create workspace files: %w", err)
	}

	fmt.Println("Workspace ready:", workspace)
	fmt.Println("Run 'nagobot serve' to start.")
	return nil
}
