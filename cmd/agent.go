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
	agentFlag    string
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
	agentCmd.Flags().StringVar(&agentFlag, "agent", "", "Override agent name (default: soul agent)")
	agentCmd.Flags().StringVar(&providerFlag, "provider", "", providerFlagHelp())
	agentCmd.Flags().StringVar(&modelFlag, "model", "", "Override model type (e.g. claude-sonnet-4-6)")
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

	mgr, err := buildThreadManager(cfg, false)
	if err != nil {
		return err
	}
	t, err := mgr.NewThread("agent", agentFlag)
	if err != nil {
		return fmt.Errorf("failed to create agent thread: %w", err)
	}
	// When --provider is explicitly set, clear the agent so resolveProvider()
	// uses the CLI-specified provider instead of per-agent model config.
	if providerFlag != "" {
		t.Agent = nil
	}

	// For one-shot agent mode, run synchronously via Enqueue + runOnce.
	var result string
	done := make(chan struct{})
	wake := &thread.WakeMessage{
		Source:  "user_message",
		Message: messageFlag,
		Sink: thread.Sink{
			Label: "your response will be printed to stdout",
			Send: func(_ context.Context, response string) error {
				result = response
				close(done)
				return nil
			},
		},
	}
	t.Enqueue(wake)
	t.RunOnce(context.Background())
	<-done

	fmt.Println(result)
	return nil
}

// applyAgentOverrides applies CLI flag overrides to config.
func applyAgentOverrides(cfg *config.Config) {
	if providerFlag != "" {
		cfg.SetProvider(providerFlag)
	}
	if modelFlag != "" {
		cfg.SetModelType(modelFlag)
	}
	if apiKeyFlag != "" {
		cfg.SetProviderAPIKey(apiKeyFlag)
	}
	if apiBaseFlag != "" {
		cfg.SetProviderAPIBase(apiBaseFlag)
	}
}
