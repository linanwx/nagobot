package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/config"
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
  nagobot set-model --default --provider deepseek --model deepseek-reasoner   # set default
  nagobot set-model --type chat --provider openai --model gpt-4o              # set routing
  nagobot set-model --type toolcall --provider anthropic --model claude-sonnet-4-20250514
  nagobot set-model --list
  nagobot set-model --type chat --clear`,
	RunE: runSetModel,
}

var (
	setModelType    string
	setModelProvider string
	setModelModel    string
	setModelList     bool
	setModelClear    bool
	setModelDefault  bool
)

func init() {
	setModelCmd.Flags().StringVar(&setModelType, "type", "", "Agent specialty declared in frontmatter (e.g. chat, toolcall)")
	setModelCmd.Flags().StringVar(&setModelProvider, "provider", "", "Target provider name")
	setModelCmd.Flags().StringVar(&setModelModel, "model", "", "Target model identifier for the provider")
	setModelCmd.Flags().BoolVar(&setModelList, "list", false, "List current model routing and agent usage")
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

	// --default: set default provider/model
	if setModelDefault {
		return setDefaultModel(cfg)
	}

	modelType := strings.TrimSpace(setModelType)
	if modelType == "" {
		return fmt.Errorf("--type or --default is required.\nFix: nagobot set-model --type <model_type> --provider <name> --model <model>\n     nagobot set-model --default --provider <name> --model <model>\nUse --list to see available model types and current routing.")
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

	pc := cfg.EnsureProviderConfigFor(provName)
	hasKey := strings.TrimSpace(pc.APIKey) != ""
	hasOAuth := provName == "openai" && cfg.Providers.OpenAIOAuth != nil && cfg.Providers.OpenAIOAuth.AccessToken != ""
	if !hasKey && !hasOAuth {
		return fmt.Errorf("provider %q has no API key configured.\nFix: nagobot set-provider-key --provider %s --api-key YOUR_KEY", provName, provName)
	}

	return nil
}

func listModelRouting(cfg *config.Config) error {
	fmt.Printf("---\ncommand: set-model\nmode: list\n---\n\nModel routing:\n")
	// Show all routing in a unified table
	fmt.Printf("  %-16s -> %s / %s (default)\n", "(default)", cfg.GetProvider(), cfg.GetModelType())
	for mt, mc := range cfg.Thread.Models {
		fmt.Printf("  %-16s -> %s / %s\n", mt, mc.Provider, mc.ModelType)
	}

	// Show agent specialty usage
	slots := scanAgentModelSlots()
	groups := groupAgentModelSlots(slots)
	if len(groups) > 0 {
		fmt.Println("\nAgent specialties:")
		for _, g := range groups {
			routing := "(default) " + cfg.GetProvider() + " / " + cfg.GetModelType()
			if mc, ok := cfg.Thread.Models[g.ModelType]; ok && mc != nil {
				routing = mc.Provider + " / " + mc.ModelType
			}
			fmt.Printf("  %-16s -> %-40s (agents: %s)\n", g.ModelType, routing, strings.Join(g.AgentNames, ", "))
		}
	}

	// Show available models per provider
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
