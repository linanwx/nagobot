package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

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
	Short: "Chat with the nagobot agent",
	Long: `Start an interactive chat session with the nagobot agent,
or send a single message with the -m flag.

Use --provider, --model, --api-key, --api-base to override config at runtime.
This allows testing different providers in parallel without editing config.json.

Examples:
  nagobot agent                                        # Interactive mode
  nagobot agent -m "Hello world"                       # Single message
  nagobot agent --provider anthropic --api-key sk-xxx -m "hi"
  nagobot agent --provider openrouter --model moonshotai/kimi-k2.5 -m "hi"`,
	RunE: runAgent,
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.Flags().StringVarP(&messageFlag, "message", "m", "", "Send a single message")
	agentCmd.Flags().StringVar(&providerFlag, "provider", "", "Override provider (openrouter, anthropic)")
	agentCmd.Flags().StringVar(&modelFlag, "model", "", "Override model type (e.g. claude-sonnet-4-5)")
	agentCmd.Flags().StringVar(&apiKeyFlag, "api-key", "", "Override API key")
	agentCmd.Flags().StringVar(&apiBaseFlag, "api-base", "", "Override API base URL")
}

func runAgent(cmd *cobra.Command, args []string) error {
	// Load config (from custom path or default)
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w\nRun 'nagobot onboard' to initialize", err)
	}

	// Apply CLI flag overrides — allows testing different providers
	// in parallel without editing config.json.
	applyAgentOverrides(cfg)

	// Create agent
	a, err := agent.NewAgent(cfg)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	ctx := context.Background()

	// Single message mode
	if messageFlag != "" {
		response, err := a.Run(ctx, messageFlag)
		if err != nil {
			return fmt.Errorf("agent error: %w", err)
		}
		fmt.Println(response)
		return nil
	}

	// Interactive mode (with session history)
	fmt.Println("nagobot interactive mode (type 'exit' or Ctrl+C to quit)")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	sessionKey := "cli:interactive"

	for {
		fmt.Print("you> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			break
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			break
		}

		response, err := a.RunInSession(ctx, sessionKey, input)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Printf("\nnagobot> %s\n\n", response)
	}

	return nil
}

// applyAgentOverrides applies CLI flag overrides to config.
// This enables parallel testing of different providers:
//
//	nagobot agent --provider anthropic --api-key sk-ant-xxx -m "hello"
//	nagobot agent --provider openrouter --api-key sk-or-xxx -m "hello"
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
