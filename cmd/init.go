package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/config"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Non-interactive setup: generate config and workspace files",
	Long: `Generate config.yaml and bootstrap workspace files without interactive prompts.

Examples:
  nagobot init --provider deepseek --model deepseek-v4-flash --api-key sk-xxx
  nagobot init --provider openrouter --api-key sk-xxx --model moonshotai/kimi-k2.5
  nagobot init --provider anthropic --model claude-sonnet-4-6 --api-key sk-xxx --telegram-token BOT_TOKEN`,
	RunE: runInit,
}

var (
	initProvider      string
	initModel         string
	initAPIKey        string
	initTelegramToken string
)

func init() {
	initCmd.Flags().StringVar(&initProvider, "provider", "deepseek", "LLM provider name")
	initCmd.Flags().StringVar(&initModel, "model", "", "Model type (defaults to provider's first supported model)")
	initCmd.Flags().StringVar(&initAPIKey, "api-key", "", "Provider API key (required)")
	initCmd.Flags().StringVar(&initTelegramToken, "telegram-token", "", "Telegram bot token (optional)")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, _ []string) error {
	apiKey := strings.TrimSpace(initAPIKey)
	if apiKey == "" {
		return fmt.Errorf("--api-key is required.\nFix: nagobot init --provider %s --api-key YOUR_KEY", initProvider)
	}

	// Load existing config or create default.
	cfg, err := config.Load()
	if err != nil {
		cfg = config.DefaultConfig()
	}

	cfg.SetProvider(initProvider)
	if initModel != "" {
		cfg.SetModelType(initModel)
	}
	cfg.SetProviderAPIKey(apiKey)

	if strings.TrimSpace(initTelegramToken) != "" {
		if cfg.Channels == nil {
			cfg.Channels = &config.ChannelsConfig{}
		}
		if cfg.Channels.Telegram == nil {
			cfg.Channels.Telegram = &config.TelegramChannelConfig{}
		}
		cfg.Channels.Telegram.Token = strings.TrimSpace(initTelegramToken)
	}

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	cmd.Printf("Configuration saved. Provider: %s", initProvider)
	if initModel != "" {
		cmd.Printf(", Model: %s", initModel)
	}
	cmd.Println()

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

	return nil
}
