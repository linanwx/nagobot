package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
)

var (
	messageFlag  string
	providerFlag string
	modelFlag    string
	apiKeyFlag   string
	apiBaseFlag  string
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Send a single message to the nagobot agent",
	Long: `Send a single message to the nagobot agent with the -m flag.
For interactive sessions, use 'nagobot serve --cli' instead.

Use --provider, --model, --api-key, --api-base to override config at runtime.

Examples:
  nagobot agent -m "Hello world"
  nagobot agent --provider anthropic --api-key sk-xxx -m "hi"
  nagobot agent --provider openrouter --model moonshotai/kimi-k2.5 -m "hi"`,
	RunE: runAgent,
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.Flags().StringVarP(&messageFlag, "message", "m", "", "Message to send (required)")
	agentCmd.Flags().StringVar(&providerFlag, "provider", "", "Override provider (openrouter, anthropic)")
	agentCmd.Flags().StringVar(&modelFlag, "model", "", "Override model type (e.g. claude-sonnet-4-5)")
	agentCmd.Flags().StringVar(&apiKeyFlag, "api-key", "", "Override API key")
	agentCmd.Flags().StringVar(&apiBaseFlag, "api-base", "", "Override API base URL")
}

func runAgent(cmd *cobra.Command, args []string) error {
	if messageFlag == "" {
		return fmt.Errorf("message is required (-m flag)\nFor interactive mode, use: nagobot serve --cli")
	}

	// Load config (from custom path or default)
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w\nRun 'nagobot onboard' to initialize", err)
	}

	// Apply CLI flag overrides
	applyAgentOverrides(cfg)

	// Create agent
	a, err := agent.NewAgent(cfg)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}
	defer a.Close()

	ctx := context.Background()

	response, err := a.Run(ctx, messageFlag)
	if err != nil {
		return fmt.Errorf("agent error: %w", err)
	}
	fmt.Println(response)
	return nil
}

// applyAgentOverrides applies CLI flag overrides to config.
func applyAgentOverrides(cfg *config.Config) {
	if providerFlag != "" {
		cfg.Agents.Defaults.Provider = providerFlag
	}
	if modelFlag != "" {
		cfg.Agents.Defaults.ModelType = modelFlag
		cfg.Agents.Defaults.ModelName = "" // reset so modelType takes effect
	}

	provider := cfg.Agents.Defaults.Provider
	if apiKeyFlag != "" {
		switch provider {
		case "openrouter":
			if cfg.Providers.OpenRouter == nil {
				cfg.Providers.OpenRouter = &config.ProviderConfig{}
			}
			cfg.Providers.OpenRouter.APIKey = apiKeyFlag
		case "anthropic":
			if cfg.Providers.Anthropic == nil {
				cfg.Providers.Anthropic = &config.ProviderConfig{}
			}
			cfg.Providers.Anthropic.APIKey = apiKeyFlag
		}
	}
	if apiBaseFlag != "" {
		switch provider {
		case "openrouter":
			if cfg.Providers.OpenRouter == nil {
				cfg.Providers.OpenRouter = &config.ProviderConfig{}
			}
			cfg.Providers.OpenRouter.APIBase = apiBaseFlag
		case "anthropic":
			if cfg.Providers.Anthropic == nil {
				cfg.Providers.Anthropic = &config.ProviderConfig{}
			}
			cfg.Providers.Anthropic.APIBase = apiBaseFlag
		}
	}
}
