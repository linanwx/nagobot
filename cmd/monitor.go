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
	monitorRefresh     bool
	monitorWindow      string
	monitorProvider    string
)

func init() {
	monitorCmd.Flags().BoolVar(&monitorBalance, "balance", false, "Check provider account balances")
	monitorCmd.Flags().BoolVar(&monitorMetrics, "metrics", false, "Show performance metrics")
	monitorCmd.Flags().BoolVar(&monitorCompression, "compression", false, "Show compression stats")
	monitorCmd.Flags().BoolVar(&monitorRefresh, "refresh", false, "Force live query instead of reading from cache (use with --balance)")
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
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}
	cachePath := filepath.Join(workspace, "system", "balance-cache.json")

	metricsDir := filepath.Join(workspace, "metrics")
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

	var results []monitor.BalanceInfo
	var updatedAt time.Time

	if !monitorRefresh {
		// Try reading from cache first.
		cached, ts, err := monitor.LoadBalance(cachePath)
		if err == nil && len(cached) > 0 {
			results = cached
			updatedAt = ts
			// Filter by provider if requested (cache contains all providers).
			if monitorProvider != "" {
				var filtered []monitor.BalanceInfo
				for _, r := range results {
					if r.Provider == monitorProvider {
						filtered = append(filtered, r)
					}
				}
				results = filtered
			}
		}
	}

	if len(results) == 0 {
		// No cache or --refresh: query live.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		results = monitor.CheckAllBalances(ctx, checkers)

		// Save to cache (only when querying all providers).
		if monitorProvider == "" {
			_ = monitor.SaveBalance(cachePath, results)
		}
	}

	fmt.Println("Provider Balances:")
	for _, r := range results {
		if r.Error != "" {
			fmt.Printf("  %-16s %s\n", r.Provider, r.Error)
			continue
		}
		for _, b := range r.Balances {
			if b.Limit > 0 {
				if b.Detail != "" {
					fmt.Printf("  %-16s %-6s %.0f/%.0f remaining  (%s)\n", r.Provider, b.Currency, b.Balance, b.Limit, b.Detail)
				} else {
					fmt.Printf("  %-16s %-6s %.0f/%.0f remaining\n", r.Provider, b.Currency, b.Balance, b.Limit)
				}
			} else if b.Detail != "" {
				fmt.Printf("  %-16s %.4f %s  (%s)\n", r.Provider, b.Balance, b.Currency, b.Detail)
			} else {
				fmt.Printf("  %-16s %.4f %s\n", r.Provider, b.Balance, b.Currency)
			}
		}
	}
	if !updatedAt.IsZero() {
		ago := time.Since(updatedAt).Round(time.Second)
		fmt.Printf("\n  (cached, last updated: %s ago)\n", formatMonitorDuration(ago))
	}
	return nil
}

func formatMonitorDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dh", h)
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
		&monitor.AnthropicBalance{KeyFn: keyFn("anthropic")},
		&monitor.DeepSeekBalance{KeyFn: keyFn("deepseek")},
		&monitor.MoonshotBalance{Name: "moonshot-cn", Base: "https://api.moonshot.cn/v1", KeyFn: keyFn("moonshot-cn")},
		&monitor.MoonshotBalance{Name: "moonshot-global", Base: "https://api.moonshot.ai/v1", KeyFn: keyFn("moonshot-global")},
		&monitor.ZhipuBalance{KeyFn: keyFn("zhipu-cn")},
		&monitor.UnsupportedBalance{Name: "gemini", Reason: "no balance API (free tier, RPD/TPM limits only)", KeyFn: keyFn("gemini")},
		&monitor.UnsupportedBalance{Name: "minimax-cn", Reason: "no balance API (pay-as-you-go; coding plan keys can use /coding_plan/remains)", KeyFn: keyFn("minimax-cn")},
	}
}
