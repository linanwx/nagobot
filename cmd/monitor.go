package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/monitor"
	"github.com/linanwx/nagobot/provider"
)

var monitorCmd = &cobra.Command{
	Use:     "monitor",
	Short:   "Check provider balances and query performance metrics",
	GroupID: "internal",
	Long: `View LLM provider account balances and performance metrics.

Examples:
  nagobot monitor --balance                # check all provider balances
  nagobot monitor --balance --provider openrouter
  nagobot monitor --metrics                # show last 24h metrics (default)
  nagobot monitor --metrics --window 1h    # show last hour
  nagobot monitor --metrics --window 7d    # show last 7 days
  nagobot monitor --compression            # show compression stats`,
	RunE: runMonitor,
}

var (
	monitorBalance     bool
	monitorMetrics     bool
	monitorCompression bool
	monitorWindow      string
	monitorProvider    string
)

func init() {
	monitorCmd.Flags().BoolVar(&monitorBalance, "balance", false, "Check provider account balances")
	monitorCmd.Flags().BoolVar(&monitorMetrics, "metrics", false, "Show performance metrics")
	monitorCmd.Flags().BoolVar(&monitorCompression, "compression", false, "Show compression stats")
	monitorCmd.Flags().StringVar(&monitorWindow, "window", "1d", "Time window for metrics: 1h, 1d, 7d")
	monitorCmd.Flags().StringVar(&monitorProvider, "provider", "", "Filter by provider name")
	rootCmd.AddCommand(monitorCmd)
}

func runMonitor(_ *cobra.Command, _ []string) error {
	if !monitorBalance && !monitorMetrics && !monitorCompression {
		return fmt.Errorf("specify --balance, --metrics, or --compression")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if monitorBalance {
		if err := showBalance(cfg); err != nil {
			return err
		}
	}

	if monitorMetrics {
		if err := showMetrics(cfg); err != nil {
			return err
		}
	}

	if monitorCompression {
		if err := showCompression(cfg); err != nil {
			return err
		}
	}

	return nil
}

func showBalance(cfg *config.Config) error {
	metricsDir := ""
	if ws, err := cfg.WorkspacePath(); err == nil {
		metricsDir = filepath.Join(ws, "metrics")
	}
	checkers := buildBalanceCheckers(cfg, metricsDir)

	if monitorProvider != "" {
		var filtered []monitor.BalanceChecker
		for _, c := range checkers {
			if c.Provider() == monitorProvider {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("unknown provider %q", monitorProvider)
		}
		checkers = filtered
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	results := monitor.CheckAllBalances(ctx, checkers)

	fmt.Println("Provider Balances:")
	for _, r := range results {
		if r.Error != "" {
			fmt.Printf("  %-16s %s\n", r.Provider, r.Error)
			continue
		}
		for _, b := range r.Balances {
			if b.Detail != "" {
				fmt.Printf("  %-16s %.4f %s  (%s)\n", r.Provider, b.Balance, b.Currency, b.Detail)
			} else {
				fmt.Printf("  %-16s %.4f %s\n", r.Provider, b.Balance, b.Currency)
			}
		}
	}
	return nil
}

func showMetrics(cfg *config.Config) error {
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	store := monitor.NewStore(filepath.Join(workspace, "metrics"))
	window := monitor.Window(strings.TrimSpace(monitorWindow))

	summary := monitor.Query(store, window)

	if summary.TotalTurns == 0 {
		fmt.Printf("No metrics recorded in the last %s.\n", monitorWindow)
		return nil
	}

	data, err := yaml.Marshal(summary)
	if err != nil {
		return fmt.Errorf("failed to format metrics: %w", err)
	}
	fmt.Println("Performance Metrics:")
	fmt.Print(string(data))
	return nil
}

func showCompression(cfg *config.Config) error {
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	store := monitor.NewStore(filepath.Join(workspace, "metrics"))
	window := monitor.Window(strings.TrimSpace(monitorWindow))
	records := store.LoadCompressions(window.Cutoff())

	fmt.Println(monitor.FormatCompressionStats(records))
	return nil
}

func buildBalanceCheckers(cfg *config.Config, metricsDir string) []monitor.BalanceChecker {
	keyFn := func(name string) func() string {
		return func() string {
			if pc := cfg.Providers.GetProviderConfig(name); pc != nil {
				return pc.APIKey
			}
			return ""
		}
	}

	return []monitor.BalanceChecker{
		func() monitor.BalanceChecker {
			cfg, _ := config.Load()
			if cfg != nil && cfg.Providers.OpenAIOAuth != nil {
				return &monitor.OpenAIQuota{
					AccessToken: cfg.Providers.OpenAIOAuth.AccessToken,
					AccountID:   cfg.Providers.OpenAIOAuth.AccountID,
				}
			}
			return &monitor.OpenAIQuota{}
		}(),
		&monitor.OpenRouterBalance{KeyFn: keyFn("openrouter")},
		&monitor.AnthropicRateLimit{
			KeyFn: keyFn("anthropic"),
			LastFn: func() *monitor.AnthropicLimits {
				rl := provider.GetAnthropicRateLimits()
				if rl == nil {
					return nil
				}
				return &monitor.AnthropicLimits{
					RequestsLimit:     rl.RequestsLimit,
					RequestsRemaining: rl.RequestsRemaining,
					TokensLimit:       rl.TokensLimit,
					TokensRemaining:   rl.TokensRemaining,
					InputLimit:        rl.InputLimit,
					InputRemaining:    rl.InputRemaining,
					OutputLimit:       rl.OutputLimit,
					OutputRemaining:   rl.OutputRemaining,
					UpdatedAt:         rl.UpdatedAt,
				}
			},
		},
		&monitor.DeepSeekBalance{KeyFn: keyFn("deepseek")},
		&monitor.MoonshotBalance{Name: "moonshot-cn", Base: "https://api.moonshot.cn/v1", KeyFn: keyFn("moonshot-cn")},
		&monitor.MoonshotBalance{Name: "moonshot-global", Base: "https://api.moonshot.ai/v1", KeyFn: keyFn("moonshot-global")},
		&monitor.ZhipuBalance{KeyFn: keyFn("zhipu-cn")},
		&monitor.UnsupportedBalance{Name: "gemini", Reason: "no balance API (free tier, RPD/TPM limits only)", KeyFn: keyFn("gemini")},
		&monitor.UnsupportedBalance{Name: "minimax-cn", Reason: "no balance API (pay-as-you-go; coding plan keys can use /coding_plan/remains)", KeyFn: keyFn("minimax-cn")},
	}
}
