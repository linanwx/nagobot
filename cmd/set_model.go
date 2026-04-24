package cmd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/monitor"
	"github.com/linanwx/nagobot/provider"
)

var setModelCmd = &cobra.Command{
	Use:     "set-model",
	Short:   "Manage model routing for agent specialties",
	GroupID: "internal",
	Long: `Configure which provider and model to use for each agent specialty.

Use --default to set the default provider/model for all agents.
Use --type to map a specific agent specialty to a different provider/model.

Agent templates declare a specialty (e.g. "chat", "toolcall") in their frontmatter.
This command maps those specialties to a specific provider and model.

Examples:
  nagobot set-model --default --provider deepseek --model deepseek-v4-flash   # set default
  nagobot set-model --type chat --provider openai --model gpt-4o              # set routing
  nagobot set-model --type toolcall --provider anthropic --model claude-sonnet-4-20250514
  nagobot set-model --list
  nagobot set-model --type chat --clear`,
	RunE: runSetModel,
}

var (
	setModelType         string
	setModelProvider     string
	setModelModel        string
	setModelList         bool
	setModelListFallback bool
	setModelClear        bool
	setModelDefault      bool
)

func init() {
	setModelCmd.Flags().StringVar(&setModelType, "type", "", "Agent specialty declared in frontmatter (e.g. chat, toolcall)")
	setModelCmd.Flags().StringVar(&setModelProvider, "provider", "", "Target provider name")
	setModelCmd.Flags().StringVar(&setModelModel, "model", "", "Target model identifier for the provider")
	setModelCmd.Flags().BoolVar(&setModelList, "list", false, "List current model routing and agent usage")
	setModelCmd.Flags().BoolVar(&setModelListFallback, "list-fallback", false, "List fallback candidates with balance and reliability status")
	setModelCmd.Flags().BoolVar(&setModelClear, "clear", false, "Remove routing for the specified model type (revert to default)")
	setModelCmd.Flags().BoolVar(&setModelDefault, "default", false, "Set the default provider/model (instead of per-type routing)")
	rootCmd.AddCommand(setModelCmd)
}

func runSetModel(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// --list: show current routing + agent usage
	if setModelList {
		return listModelRouting(cfg)
	}

	// --list-fallback: show fallback candidates with balance status
	if setModelListFallback {
		return listFallbackStatus(cfg)
	}

	// --default: set default provider/model
	if setModelDefault {
		return setDefaultModel(cfg)
	}

	modelType := strings.TrimSpace(setModelType)
	if modelType == "" {
		return fmt.Errorf("--type or --default is required.\nFix: nagobot set-model --type <model_type> --provider <name> --model <model>\n     nagobot set-model --default --provider <name> --model <model>\nUse --list to see available model types and current routing.")
	}

	if modelType == "default" {
		return fmt.Errorf("use --default flag instead of --type default.\nFix: nagobot set-model --default --provider <name> --model <model>")
	}

	// --clear: remove routing for this model type
	if setModelClear {
		if cfg.Thread.Models != nil {
			delete(cfg.Thread.Models, modelType)
		}
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Printf("---\ncommand: set-model\nstatus: ok\ntype: %s\naction: cleared\n---\n\nCleared model routing for type %q (will use default: %s/%s).\n", modelType, modelType, cfg.GetProvider(), cfg.GetModelType())
		return nil
	}

	provName := strings.TrimSpace(setModelProvider)
	modelName := strings.TrimSpace(setModelModel)

	if provName == "" || modelName == "" {
		return fmt.Errorf("--provider and --model are required.\nFix: nagobot set-model --type %s --provider <name> --model <model>", modelType)
	}

	if err := validateProviderModel(cfg, provName, modelName, fmt.Sprintf("nagobot set-model --type %s", modelType)); err != nil {
		return err
	}

	// Set routing
	if cfg.Thread.Models == nil {
		cfg.Thread.Models = make(map[string]*config.ModelConfig)
	}
	cfg.Thread.Models[modelType] = &config.ModelConfig{
		Provider:  provName,
		ModelType: modelName,
	}

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Printf("---\ncommand: set-model\nstatus: ok\ntype: %s\nprovider: %s\nmodel: %s\n---\n\nSet model routing: type %q -> %s/%s.\n", modelType, provName, modelName, modelType, provName, modelName)
	return nil
}

func setDefaultModel(cfg *config.Config) error {
	provName := strings.TrimSpace(setModelProvider)
	modelName := strings.TrimSpace(setModelModel)

	if provName == "" || modelName == "" {
		return fmt.Errorf("--provider and --model are required.\nFix: nagobot set-model --provider <name> --model <model>\nOr use --type to set routing for a specific model type.")
	}

	if err := validateProviderModel(cfg, provName, modelName, "nagobot set-model"); err != nil {
		return err
	}

	cfg.SetProvider(provName)
	cfg.SetModelType(modelName)

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Printf("---\ncommand: set-model\nstatus: ok\ntype: default\nprovider: %s\nmodel: %s\n---\n\nSet default model: %s / %s.\n", provName, modelName, provName, modelName)
	return nil
}

func validateProviderModel(cfg *config.Config, provName, modelName, cmdPrefix string) error {
	supported := provider.SupportedProviders()

	if !isSupported(provName, supported) {
		return fmt.Errorf("unknown provider %q.\nSupported providers: %s\nFix: %s --provider <name> --model <model>", provName, strings.Join(supported, ", "), cmdPrefix)
	}

	if err := provider.ValidateProviderModelType(provName, modelName); err != nil {
		models := provider.SupportedModelsForProvider(provName)
		return fmt.Errorf("%w\nSupported models for %s: %s\nFix: %s --provider %s --model <model>", err, provName, strings.Join(models, ", "), cmdPrefix, provName)
	}

	pc := cfg.Providers.GetProviderConfig(provName)
	hasKey := pc != nil && strings.TrimSpace(pc.APIKey) != ""
	oauthToken := cfg.GetOAuthToken(provName)
	hasOAuth := oauthToken != nil && oauthToken.AccessToken != ""
	if !hasKey && !hasOAuth {
		return fmt.Errorf("provider %q has no API key configured.\nFix: nagobot set-provider-key --provider %s --api-key YOUR_KEY", provName, provName)
	}

	return nil
}

func listModelRouting(cfg *config.Config) error {
	cfgPath, _ := config.ConfigPath()
	fmt.Printf("---\ncommand: set-model\nmode: list\n---\n")

	// Model routing table with source file
	fmt.Printf("\nModel routing (from %s):\n", cfgPath)
	fmt.Printf("  %-20s %s / %s (default)\n", "(default)", cfg.GetProvider(), cfg.GetModelType())
	for mt, mc := range cfg.Thread.Models {
		fmt.Printf("  %-20s %s / %s\n", mt, mc.Provider, mc.ModelType)
	}

	// Agent routing: all agents, right-joined with routing table
	fmt.Printf("\nAgent routing:\n")
	fmt.Printf("  %-20s %-20s %s\n", "Agent", "Specialty", "Provider / Model")
	fmt.Printf("  %-20s %-20s %s\n", "─────", "─────────", "────────────────")

	allAgents := scanAllAgents()
	defaultLabel := cfg.GetProvider() + " / " + cfg.GetModelType()
	for _, a := range allAgents {
		specialty := a.ModelType
		if specialty == "" {
			specialty = "(none)"
		}
		routingLabel := defaultLabel + " (default)"
		if a.ModelType != "" {
			if mc, ok := cfg.Thread.Models[a.ModelType]; ok && mc != nil {
				routingLabel = mc.Provider + " / " + mc.ModelType
			}
		}
		fmt.Printf("  %-20s %-20s %s\n", a.AgentName, specialty, routingLabel)
	}

	// Implicit specialty routing: "provider/model" specialties that auto-route without config.
	// Only shows providers with configured API keys.
	fmt.Println("\nImplicit specialty routing (use as agent specialty, no config needed):")
	for _, prov := range provider.SupportedProviders() {
		pc := cfg.Providers.GetProviderConfig(prov)
		hasKey := pc != nil && strings.TrimSpace(pc.APIKey) != ""
		oauthTok := cfg.GetOAuthToken(prov)
		hasOAuth := oauthTok != nil && oauthTok.AccessToken != ""
		if !hasKey && !hasOAuth {
			continue // skip unconfigured providers
		}
		models := provider.SupportedModelsForProvider(prov)
		for _, m := range models {
			specialty := prov + "/" + m
			// Skip if already in explicit routing table
			if _, ok := cfg.Thread.Models[specialty]; ok {
				continue
			}
			ctx := provider.ContextWindowForModel(m)
			if ctx > 0 {
				fmt.Printf("  %-40s → %s / %s (%s)\n", specialty, prov, m, formatContextTokens(ctx))
			} else {
				fmt.Printf("  %-40s → %s / %s\n", specialty, prov, m)
			}
		}
	}

	// Available models per provider
	fmt.Println("\nAvailable models:")
	for _, prov := range provider.SupportedProviders() {
		models := provider.SupportedModelsForProvider(prov)
		if len(models) == 0 {
			continue
		}
		fmt.Printf("  %s:\n", prov)
		for _, m := range models {
			ctx := provider.ContextWindowForModel(m)
			if ctx > 0 {
				fmt.Printf("    %-40s %s\n", m, formatContextTokens(ctx))
			} else {
				fmt.Printf("    %s\n", m)
			}
		}
	}

	return nil
}

// scanAllAgents reads all embedded agent templates, returning every agent
// with its specialty (empty string if none declared).
func scanAllAgents() []agentModelSlot {
	entries, err := templateFS.ReadDir("templates/agents")
	if err != nil {
		return nil
	}
	var slots []agentModelSlot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := templateFS.ReadFile("templates/agents/" + e.Name())
		if err != nil {
			continue
		}
		meta, _, _, _ := agent.ParseTemplate(string(raw))
		name := strings.TrimSpace(meta.Name)
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		slots = append(slots, agentModelSlot{
			AgentName: name,
			ModelType: strings.TrimSpace(meta.Specialty),
		})
	}
	sort.Slice(slots, func(i, j int) bool {
		return slots[i].AgentName < slots[j].AgentName
	})
	return slots
}

func listFallbackStatus(cfg *config.Config) error {
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	// Load cached balance data.
	cachePath := filepath.Join(workspace, "system", "balance-cache.json")
	balances, updatedAt, _ := monitor.LoadBalance(cachePath)

	// Index balance entries by provider name.
	balanceMap := make(map[string]*monitor.BalanceInfo, len(balances))
	for i := range balances {
		balanceMap[balances[i].Provider] = &balances[i]
	}

	// Classify each configured provider.
	type providerStatus struct {
		name    string
		models  []string
		balance *monitor.BalanceInfo
	}

	var available, exhausted, unreliable []providerStatus

	for _, prov := range provider.SupportedProviders() {
		if !provider.ProviderKeyAvailable(cfg, prov) {
			continue // no API key, skip entirely
		}
		models := provider.SupportedModelsForProvider(prov)
		if len(models) == 0 {
			continue
		}

		ps := providerStatus{name: prov, models: models, balance: balanceMap[prov]}

		bi := ps.balance
		if bi.IsUnreliable() {
			unreliable = append(unreliable, ps)
		} else if bi.IsExhausted() {
			exhausted = append(exhausted, ps)
		} else {
			available = append(available, ps)
		}
	}

	fmt.Printf("---\ncommand: set-model\nmode: list-fallback\n---\n")

	// Section 1: Available fallback candidates.
	fmt.Println("\nFallback candidates (available):")
	if len(available) == 0 {
		fmt.Println("  (none)")
	}
	for _, ps := range available {
		printProviderModels(ps.name, ps.models, ps.balance)
	}

	// Section 2: Balance exhausted.
	fmt.Println("\nBalance exhausted:")
	if len(exhausted) == 0 {
		fmt.Println("  (none)")
	}
	for _, ps := range exhausted {
		printProviderModels(ps.name, ps.models, ps.balance)
	}

	// Section 3: Unreliable (cannot check balance).
	fmt.Println("\nUnreliable (cannot verify balance):")
	if len(unreliable) == 0 {
		fmt.Println("  (none)")
	}
	for _, ps := range unreliable {
		reason := ""
		if ps.balance != nil && ps.balance.Error != "" {
			reason = ps.balance.Error
		} else if ps.balance != nil && len(ps.balance.Balances) > 0 {
			reason = "no balance API — rate limits only"
		} else {
			reason = "no balance data"
		}
		fmt.Printf("  %s  [%s]\n", ps.name, reason)
		for _, m := range ps.models {
			ctx := provider.ContextWindowForModel(m)
			if ctx > 0 {
				fmt.Printf("    %-40s %s\n", m, formatContextTokens(ctx))
			} else {
				fmt.Printf("    %s\n", m)
			}
		}
	}

	// Cache freshness.
	if !updatedAt.IsZero() {
		ago := time.Since(updatedAt).Round(time.Second)
		fmt.Printf("\n  (balance cache: %s ago)\n", formatMonitorDuration(ago))
	} else {
		fmt.Println("\n  (no balance cache — run 'nagobot monitor --balance --refresh' or start serve)")
	}

	return nil
}


func printProviderModels(name string, models []string, bi *monitor.BalanceInfo) {
	balanceStr := ""
	if bi != nil {
		parts := []string{}
		for _, b := range bi.Balances {
			switch b.Currency {
			case "plan", "status":
				continue
			}
			if b.Limit > 0 {
				parts = append(parts, fmt.Sprintf("%.0f/%.0f %s", b.Balance, b.Limit, b.Currency))
			} else if b.Balance != 0 {
				// Use integer format for large values, decimal for small.
				if b.Balance >= 100 {
					parts = append(parts, fmt.Sprintf("%.0f %s", b.Balance, b.Currency))
				} else {
					parts = append(parts, fmt.Sprintf("%.2f %s", b.Balance, b.Currency))
				}
			}
		}
		if len(parts) > 0 {
			balanceStr = "  [" + strings.Join(parts, ", ") + "]"
		}
	}
	fmt.Printf("  %s%s\n", name, balanceStr)
	for _, m := range models {
		ctx := provider.ContextWindowForModel(m)
		if ctx > 0 {
			fmt.Printf("    %-40s %s\n", m, formatContextTokens(ctx))
		} else {
			fmt.Printf("    %s\n", m)
		}
	}
}

func formatContextTokens(tokens int) string {
	if tokens >= 1000000 {
		v := float64(tokens) / 1048576
		if v == float64(int(v)) {
			return fmt.Sprintf("%dM", int(v))
		}
		return fmt.Sprintf("%.1fM", v)
	}
	return fmt.Sprintf("%dK", tokens/1000)
}
