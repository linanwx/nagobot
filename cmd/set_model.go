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
	"github.com/linanwx/nagobot/tools"
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
		cfg.Thread.Models = config.RemoveModelRule(cfg.Thread.Models, config.ModelRuleSpecialty, modelType)
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Print(tools.CmdOutput([][2]string{
			{"command", "set-model"}, {"status", "ok"}, {"type", modelType}, {"action", "cleared"},
		}, fmt.Sprintf("Cleared model routing for type %q (will use default: %s/%s).", modelType, cfg.GetProvider(), cfg.GetModelType())) + "\n")
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

	// Set routing: upsert a specialty rule.
	cfg.Thread.Models = config.UpsertModelRule(cfg.Thread.Models, config.ModelRuleSpecialty, modelType, provName, modelName)

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "set-model"}, {"status", "ok"}, {"type", modelType}, {"provider", provName}, {"model", modelName},
	}, fmt.Sprintf("Set model routing: type %q -> %s/%s.", modelType, provName, modelName)) + "\n")
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
	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "set-model"}, {"status", "ok"}, {"type", "default"}, {"provider", provName}, {"model", modelName},
	}, fmt.Sprintf("Set default model: %s / %s.", provName, modelName)) + "\n")
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
	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "set-model"}, {"mode", "list"},
	}, ""))

	// Model routing table with source file
	fmt.Printf("\nModel routing (from %s):\n", cfgPath)
	fmt.Printf("  %-28s %s / %s (default)\n", "(default)", cfg.GetProvider(), cfg.GetModelType())
	for _, r := range cfg.Thread.Models {
		fmt.Printf("  %-28s %s / %s\n", r.Type+":"+r.Name, r.Provider, r.ModelType)
	}

	// Agent routing: one row per agent showing the ACTUAL resolved model —
	// agent rule > first specialty (left-to-right) with a rule > default. A
	// non-matching specialty falls through to the next, so multi-specialty
	// agents do not show "default" just because an earlier specialty is unset.
	fmt.Printf("\nAgent routing:\n")
	fmt.Printf("  %-20s %-20s %s\n", "Agent", "Specialty", "Provider / Model")
	fmt.Printf("  %-20s %-20s %s\n", "─────", "─────────", "────────────────")

	defaultLabel := cfg.GetProvider() + " / " + cfg.GetModelType()
	// firstSpecialtyRule returns the model label for the first specialty in the
	// list that has a type:specialty rule, mirroring thread/run.go's
	// left-to-right resolution. Empty second return = no specialty matched.
	firstSpecialtyRule := func(specialties []string) (label, via string) {
		for _, sp := range specialties {
			if r := config.FindModelRule(cfg.Thread.Models, config.ModelRuleSpecialty, sp); r != nil {
				return r.Provider + " / " + r.ModelType, sp
			}
		}
		return "", ""
	}

	for _, a := range scanAllAgents() {
		specialtyLabel := strings.Join(a.Specialties, ", ")
		if specialtyLabel == "" {
			specialtyLabel = "(none)"
		}
		// Main row = the non-source chain: agent rule > first basic specialty
		// with a rule > default. Source-specialty routing sits ABOVE this in
		// precedence but only on matching wake sources, so it is shown as
		// sub-rows below rather than folded into this model.
		routingLabel := defaultLabel + " (default)"
		if r := config.FindModelRule(cfg.Thread.Models, config.ModelRuleAgent, a.Name); r != nil {
			routingLabel = r.Provider + " / " + r.ModelType + " (agent rule)"
		} else if label, via := firstSpecialtyRule(a.Specialties); label != "" {
			routingLabel = label
			if len(a.Specialties) > 1 {
				routingLabel += " (via " + via + ")"
			}
		}
		fmt.Printf("  %-20s %-20s %s\n", a.Name, specialtyLabel, routingLabel)

		// Source-specialty overrides: one sub-row per wake source, sorted for
		// deterministic output. Each shows what that source's specialty list
		// resolves to; a list with no matching rule cascades to the main row's
		// model (agent rule / basic specialty / default), flagged as such.
		for _, src := range sortedKeys(a.SourceSpecialties) {
			list := a.SourceSpecialties[src]
			label, via := firstSpecialtyRule(list)
			model := label
			if model == "" {
				model = routingLabel + " (cascades)"
			} else if len(list) > 1 {
				model += " (via " + via + ")"
			}
			fmt.Printf("    ↳ on %-14s %-20s %s\n", src, strings.Join(list, ", "), model)
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
			ctx := provider.ContextWindowForModel(prov, m)
			if ctx > 0 {
				fmt.Printf("    %-40s %s\n", m, formatContextTokens(ctx))
			} else {
				fmt.Printf("    %s\n", m)
			}
		}
	}

	return nil
}

// sortedKeys returns a map's keys in ascending order, for deterministic output.
func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// agentSpecialties is an agent paired with its ordered specialty tags.
type agentSpecialties struct {
	Name        string
	Specialties []string
	// SourceSpecialties maps a wake source (e.g. "heartbeat") to the specialty
	// list tried, in precedence, ABOVE the agent rule and the basic Specialties
	// — but only on turns whose wake source matches. --list has no live wake
	// source, so these are rendered as extra sub-rows rather than folded into
	// the agent's main resolved model.
	SourceSpecialties map[string][]string
}

// scanAllAgents reads all embedded agent templates, returning each agent with
// its specialty list in frontmatter order (order matters for left-to-right
// model resolution).
func scanAllAgents() []agentSpecialties {
	entries, err := templateFS.ReadDir("templates/agents")
	if err != nil {
		return nil
	}
	var out []agentSpecialties
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
		var srcSpec map[string][]string
		if len(meta.SourceSpecialties) > 0 {
			srcSpec = make(map[string][]string, len(meta.SourceSpecialties))
			for src, list := range meta.SourceSpecialties {
				srcSpec[src] = []string(list)
			}
		}
		out = append(out, agentSpecialties{
			Name:              name,
			Specialties:       []string(meta.Specialties),
			SourceSpecialties: srcSpec,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
			ctx := provider.ContextWindowForModel(ps.name, m)
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
		ctx := provider.ContextWindowForModel(name, m)
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
