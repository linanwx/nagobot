package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show nagobot configuration status",
	Long:  `Display the current nagobot configuration and status.`,
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("Status: Not configured")
		fmt.Println()
		fmt.Println("Run 'nagobot onboard' to initialize nagobot.")
		return nil
	}

	fmt.Println("nagobot Status")
	fmt.Println("==============")
	fmt.Println()

	// Config path
	configPath, _ := config.ConfigPath()
	fmt.Println("Config:", configPath)

	// Workspace
	workspace, _ := cfg.WorkspacePath()
	fmt.Println("Workspace:", workspace)
	fmt.Println()

	// Provider
	providerName := cfg.GetProvider()
	modelType := cfg.GetModelType()
	modelName := cfg.GetModelName()
	fmt.Println("Provider:", providerName)
	fmt.Println("Model Type:", modelType)
	if modelName != "" && modelName != modelType {
		fmt.Println("Model Name:", modelName)
	}
	fmt.Println()

	// Validate model type
	if err := provider.ValidateProviderModelType(
		providerName,
		modelType,
	); err != nil {
		fmt.Println("Warning:", err)
		fmt.Println()
	}

	// API key status
	_, apiErr := cfg.GetAPIKey()
	if apiErr != nil {
		fmt.Println("API Key: NOT CONFIGURED")
		fmt.Println()
		fmt.Println("Add your API key to the config file.")
	} else {
		fmt.Println("API Key: Configured")
	}

	fmt.Println()
	fmt.Println("Settings:")
	fmt.Printf("  Max Tokens: %d\n", cfg.GetMaxTokens())
	fmt.Printf("  Temperature: %.1f\n", cfg.GetTemperature())
	effectiveCtx := provider.EffectiveContextWindow(cfg.GetModelName(), cfg.GetContextWindowTokens())
	fmt.Printf("  Context Window Tokens: %d (effective: %d)\n", cfg.GetContextWindowTokens(), effectiveCtx)
	fmt.Printf("  Context Warn Ratio: %.2f\n", cfg.GetContextWarnRatio())

	return nil
}
