package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread"
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
	agentCmd.Flags().StringVar(&providerFlag, "provider", "", providerFlagHelp())
	agentCmd.Flags().StringVar(&modelFlag, "model", "", "Override model type (e.g. claude-sonnet-4-5)")
	agentCmd.Flags().StringVar(&apiKeyFlag, "api-key", "", "Override API key")
	agentCmd.Flags().StringVar(&apiBaseFlag, "api-base", "", "Override API base URL")
}

func providerFlagHelp() string {
	names := provider.SupportedProviders()
	if len(names) == 0 {
		return "Override provider"
	}
	return fmt.Sprintf("Override provider (%s)", strings.Join(names, ", "))
}

func runAgent(cmd *cobra.Command, args []string) error {
	if messageFlag == "" {
		return fmt.Errorf("message is required (-m flag)\nFor interactive mode, use: nagobot serve --cli")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w\nRun 'nagobot onboard' to initialize", err)
	}

	applyAgentOverrides(cfg)

	tcfg, err := buildThreadConfig(cfg, false)
	if err != nil {
		return err
	}

	mgr := thread.NewManager(tcfg)
	t := mgr.NewThread("agent", "")

	// For one-shot agent mode, run synchronously via Enqueue + runOnce.
	var result string
	done := make(chan struct{})
	t.Enqueue(&thread.WakeMessage{
		Source:  "user_message",
		Message: messageFlag,
		Sink: func(_ context.Context, response string) error {
			result = response
			close(done)
			return nil
		},
	})
	t.RunOnce(context.Background())
	<-done

	fmt.Println(result)
	return nil
}

// applyAgentOverrides applies CLI flag overrides to config.
func applyAgentOverrides(cfg *config.Config) {
	if providerFlag != "" {
		cfg.Thread.Provider = providerFlag
	}
	if modelFlag != "" {
		cfg.Thread.ModelType = modelFlag
		cfg.Thread.ModelName = "" // reset so modelType takes effect
	}

	provider := cfg.GetProvider()
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
		case "deepseek":
			if cfg.Providers.DeepSeek == nil {
				cfg.Providers.DeepSeek = &config.ProviderConfig{}
			}
			cfg.Providers.DeepSeek.APIKey = apiKeyFlag
		case "moonshot-cn":
			if cfg.Providers.MoonshotCN == nil {
				cfg.Providers.MoonshotCN = &config.ProviderConfig{}
			}
			cfg.Providers.MoonshotCN.APIKey = apiKeyFlag
		case "moonshot-global":
			if cfg.Providers.MoonshotGlobal == nil {
				cfg.Providers.MoonshotGlobal = &config.ProviderConfig{}
			}
			cfg.Providers.MoonshotGlobal.APIKey = apiKeyFlag
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
		case "deepseek":
			if cfg.Providers.DeepSeek == nil {
				cfg.Providers.DeepSeek = &config.ProviderConfig{}
			}
			cfg.Providers.DeepSeek.APIBase = apiBaseFlag
		case "moonshot-cn":
			if cfg.Providers.MoonshotCN == nil {
				cfg.Providers.MoonshotCN = &config.ProviderConfig{}
			}
			cfg.Providers.MoonshotCN.APIBase = apiBaseFlag
		case "moonshot-global":
			if cfg.Providers.MoonshotGlobal == nil {
				cfg.Providers.MoonshotGlobal = &config.ProviderConfig{}
			}
			cfg.Providers.MoonshotGlobal.APIBase = apiBaseFlag
		}
	}
}
