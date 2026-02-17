package cmd

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
)

//go:embed templates/*
var templateFS embed.FS

var onboardCmd = &cobra.Command{
	Use:   "onboard",
	Short: "Initialize nagobot configuration and workspace",
	Long:  `Create or reconfigure nagobot configuration and workspace interactively.`,
	RunE:  runOnboard,
}

func init() {
	rootCmd.AddCommand(onboardCmd)
}

// providerURLs maps provider names to their API key portal URLs.
var providerURLs = map[string]string{
	"openai":          "https://platform.openai.com/api-keys",
	"deepseek":        "https://platform.deepseek.com",
	"openrouter":      "https://openrouter.ai/keys",
	"anthropic":       "https://console.anthropic.com",
	"moonshot-cn":     "https://platform.moonshot.cn",
	"moonshot-global": "https://platform.moonshot.ai",
	"zhipu-cn":        "https://open.bigmodel.cn",
	"zhipu-global":    "https://z.ai",
	"minimax-cn":      "https://platform.minimaxi.com",
	"minimax-global":  "https://platform.minimax.io",
}

func runOnboard(_ *cobra.Command, _ []string) error {
	configPath, err := config.ConfigPath()
	if err != nil {
		return err
	}

	// Load existing config as defaults, or start fresh.
	existing, _ := config.Load()
	if existing == nil {
		existing = config.DefaultConfig()
	}
	defaults := loadOnboardDefaults(existing)

	// --- interactive wizard ---

	var (
		selectedProvider = defaults.provider
		selectedModel    = defaults.model
		configureTG      bool
	)

	// Step 1+2: select default provider + model
	if defaults.provider != "" && defaults.model != "" {
		keepDefault := true
		err = huh.NewForm(huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Keep current default (%s/%s)?", defaults.provider, defaults.model)).
				Value(&keepDefault),
		)).Run()
		if err != nil {
			return err
		}
		if !keepDefault {
			selectedProvider = ""
			selectedModel = ""
		}
	}
	if selectedProvider == "" || selectedModel == "" {
		providerOptions := buildProviderOptions()
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Choose your default LLM provider").
					Description("nagobot supports multiple LLM providers. Choose one to get started.").
					Options(providerOptions...).
					Value(&selectedProvider),
			),
		).Run()
		if err != nil {
			return err
		}
		if selectedProvider != defaults.provider {
			selectedModel = ""
		}
		modelOptions := buildModelOptions(selectedProvider)
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Choose default model for "+selectedProvider).
					Description("Only whitelisted models are supported. The first option is the recommended default.").
					Options(modelOptions...).
					Value(&selectedModel),
			),
		).Run()
		if err != nil {
			return err
		}
	}

	// Step 3: Per-agent model override
	defaultLabel := selectedProvider + " / " + selectedModel
	slots := scanAgentModelSlots()
	modelOverrides := make(map[string]*config.ModelConfig)
	for _, slot := range slots {
		// Check existing override from prior onboard.
		var existingMC *config.ModelConfig
		if existing.Thread.Models != nil {
			existingMC = existing.Thread.Models[slot.ModelType]
		}
		hasExistingOverride := existingMC != nil &&
			(existingMC.Provider != selectedProvider || existingMC.ModelType != selectedModel)

		if !hasExistingOverride {
			// No prior override: 2-choice (use default / pick new).
			useDefault := true
			err = huh.NewForm(huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Agent '%s' (%s): use default (%s)?", slot.AgentName, slot.ModelType, defaultLabel)).
					Value(&useDefault),
			)).Run()
			if err != nil {
				return err
			}
			if useDefault {
				modelOverrides[slot.ModelType] = &config.ModelConfig{
					Provider: selectedProvider, ModelType: selectedModel,
				}
				continue
			}
		} else {
			// Has prior override: 3-choice (keep / default / new).
			choice := "keep"
			currentLabel := existingMC.Provider + " / " + existingMC.ModelType
			err = huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title(fmt.Sprintf("Agent '%s' (%s):", slot.AgentName, slot.ModelType)).
					Options(
						huh.NewOption("Keep current ("+currentLabel+")", "keep"),
						huh.NewOption("Use default ("+defaultLabel+")", "default"),
						huh.NewOption("Choose different", "new"),
					).
					Value(&choice),
			)).Run()
			if err != nil {
				return err
			}
			if choice == "keep" {
				modelOverrides[slot.ModelType] = existingMC
				continue
			}
			if choice == "default" {
				modelOverrides[slot.ModelType] = &config.ModelConfig{
					Provider: selectedProvider, ModelType: selectedModel,
				}
				continue
			}
		}

		// Pick new provider + model for this agent.
		var overrideProvider string
		err = huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title(fmt.Sprintf("Choose provider for '%s' (%s)", slot.AgentName, slot.ModelType)).
				Options(buildProviderOptions()...).
				Value(&overrideProvider),
		)).Run()
		if err != nil {
			return err
		}
		var overrideModel string
		err = huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title(fmt.Sprintf("Choose model for '%s' (%s)", slot.AgentName, slot.ModelType)).
				Options(buildModelOptions(overrideProvider)...).
				Value(&overrideModel),
		)).Run()
		if err != nil {
			return err
		}
		modelOverrides[slot.ModelType] = &config.ModelConfig{
			Provider: overrideProvider, ModelType: overrideModel,
		}
	}

	// Step 4: Authentication for all unique providers.
	uniqueProviders := collectUniqueProviders(selectedProvider, modelOverrides)
	for _, provName := range uniqueProviders {
		if err := authenticateProvider(existing, provName); err != nil {
			return err
		}
	}

	// Step 5: optional Telegram
	configureTG = defaults.tgToken != ""
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Configure Telegram bot?").
				Description("You can skip and configure later in config.yaml.").
				Value(&configureTG),
		),
	).Run()
	if err != nil {
		return err
	}

	tgToken := defaults.tgToken
	tgAdminID := defaults.tgAdminID
	tgAllowedIDs := defaults.tgAllowedIDs
	if configureTG {
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Telegram Bot Token").
					Description("Open @BotFather on Telegram, run /newbot, and paste the token here.").
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return fmt.Errorf("bot token is required")
						}
						return nil
					}).
					Value(&tgToken),
				huh.NewInput().
					Title("Admin User ID").
					Description("Open @userinfobot on Telegram, send /start, and paste your numeric user ID here.").
					Value(&tgAdminID),
				huh.NewInput().
					Title("Allowed User IDs").
					Description("Open @userinfobot for each user, paste their IDs comma-separated. Leave empty to allow all.").
					Value(&tgAllowedIDs),
			),
		).Run()
		if err != nil {
			return err
		}
	}

	// --- apply config ---

	// Start from existing config to preserve all provider keys and settings.
	cfg := existing
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	cfg.SetProvider(selectedProvider)
	cfg.SetModelType(selectedModel)
	if cfg.Thread.Models == nil {
		cfg.Thread.Models = make(map[string]*config.ModelConfig)
	}
	for k, v := range modelOverrides {
		cfg.Thread.Models[k] = v
	}

	if configureTG {
		cfg.Channels.AdminUserID = strings.TrimSpace(tgAdminID)
		cfg.Channels.Telegram.Token = strings.TrimSpace(tgToken)
		cfg.Channels.Telegram.AllowedIDs = parseAllowedIDs(tgAllowedIDs)
	}

	// --- create directories and files ---

	configDir, err := config.ConfigDir()
	if err != nil {
		return fmt.Errorf("failed to determine config directory: %w", err)
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	if err := cfg.EnsureWorkspace(); err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to determine workspace path: %w", err)
	}
	if err := createBootstrapFiles(workspace); err != nil {
		return fmt.Errorf("failed to create bootstrap files: %w", err)
	}
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println()
	fmt.Println("nagobot initialized successfully!")
	fmt.Println()
	fmt.Println("  Config:", configPath)
	fmt.Println("  Workspace:", workspace)
	fmt.Println("  Default:", selectedProvider+"/"+selectedModel)
	if len(cfg.Thread.Models) > 0 {
		fmt.Println("  Models:")
		for modelType, mc := range cfg.Thread.Models {
			fmt.Printf("    %s: %s/%s\n", modelType, mc.Provider, mc.ModelType)
		}
	}
	fmt.Println()
	fmt.Println("Run 'nagobot serve' to start.")
	return nil
}

// onboardDefaults holds pre-filled values from existing config.
type onboardDefaults struct {
	provider     string
	model        string
	tgToken      string
	tgAdminID    string
	tgAllowedIDs string
}

func loadOnboardDefaults(cfg *config.Config) onboardDefaults {
	if cfg == nil {
		return onboardDefaults{}
	}
	return onboardDefaults{
		provider:      cfg.GetProvider(),
		model:         cfg.GetModelType(),
		tgToken:       cfg.GetTelegramToken(),
		tgAdminID:     cfg.GetAdminUserID(),
		tgAllowedIDs: formatAllowedIDs(cfg.GetTelegramAllowedIDs()),
	}
}

func formatAllowedIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ", ")
}

func buildProviderOptions() []huh.Option[string] {
	names := provider.SupportedProviders()
	// Put deepseek first.
	sorted := make([]string, 0, len(names))
	for _, n := range names {
		if n == "deepseek" {
			sorted = append([]string{n}, sorted...)
		} else {
			sorted = append(sorted, n)
		}
	}
	options := make([]huh.Option[string], 0, len(sorted))
	for _, name := range sorted {
		models := provider.SupportedModelsForProvider(name)
		label := name + " (" + strings.Join(models, ", ") + ")"
		if name == "deepseek" {
			label += " [Recommended]"
		}
		options = append(options, huh.NewOption(label, name))
	}
	return options
}

func buildModelOptions(providerName string) []huh.Option[string] {
	models := provider.SupportedModelsForProvider(providerName)
	options := make([]huh.Option[string], 0, len(models))
	for _, m := range models {
		options = append(options, huh.NewOption(m, m))
	}
	return options
}

func parseAllowedIDs(raw string) []int64 {
	var ids []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// writeTemplate writes an embedded template file to the workspace.
// If overwrite is false, existing files are skipped.
func writeTemplate(workspace, templateName, destName string, overwrite bool) error {
	destPath := filepath.Join(workspace, destName)
	if !overwrite {
		if _, err := os.Stat(destPath); err == nil {
			return nil
		}
	}
	data, err := templateFS.ReadFile("templates/" + templateName)
	if err != nil {
		return fmt.Errorf("read embedded template %s: %w", templateName, err)
	}
	return os.WriteFile(destPath, data, 0644)
}

func createBootstrapFiles(workspace string) error {
	const (
		skillsDir   = "skills"
		sessionsDir = "sessions"
	)

	for _, dir := range []string{
		".tmp",
		"agents",
		"bin",
		"docs",
		skillsDir,
		filepath.Join(sessionsDir, "main"),
		filepath.Join(sessionsDir, "cron"),
	} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0755); err != nil {
			return err
		}
	}

	// Root-level templates; skip if they already exist.
	for _, name := range []string{"USER.md", "CORE_MECHANISM.md"} {
		if err := writeTemplate(workspace, name, name, false); err != nil {
			return err
		}
	}

	// Copy embedded agent and skill directories into workspace.
	if err := copyEmbeddedDir("templates/agents", filepath.Join(workspace, "agents")); err != nil {
		return err
	}
	if err := copyEmbeddedDir("templates/skills", filepath.Join(workspace, skillsDir)); err != nil {
		return err
	}

	return nil
}

// agentModelSlot represents an agent that declares a model type in its frontmatter.
type agentModelSlot struct {
	AgentName string
	ModelType string
}

// scanAgentModelSlots reads embedded agent templates and returns those with a model: field.
func scanAgentModelSlots() []agentModelSlot {
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
		if strings.TrimSpace(meta.Model) != "" {
			name := strings.TrimSpace(meta.Name)
			if name == "" {
				name = strings.TrimSuffix(e.Name(), ".md")
			}
			slots = append(slots, agentModelSlot{
				AgentName: name,
				ModelType: strings.TrimSpace(meta.Model),
			})
		}
	}
	sort.Slice(slots, func(i, j int) bool {
		return slots[i].AgentName < slots[j].AgentName
	})
	return slots
}

// collectUniqueProviders returns a sorted list of unique provider names
// from the default provider and all model overrides.
func collectUniqueProviders(defaultProv string, models map[string]*config.ModelConfig) []string {
	seen := map[string]bool{defaultProv: true}
	for _, mc := range models {
		if mc != nil {
			seen[mc.Provider] = true
		}
	}
	providers := make([]string, 0, len(seen))
	for p := range seen {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	return providers
}

// authenticateProvider handles authentication for a single provider,
// supporting both OAuth and API key flows.
func authenticateProvider(existing *config.Config, providerName string) error {
	// Check existing OAuth token.
	hasOAuth := existing.GetOAuthToken(providerName) != nil &&
		existing.GetOAuthToken(providerName).AccessToken != ""

	// Check existing API key.
	pc := existing.EnsureProviderConfigFor(providerName)
	hasAPIKey := strings.TrimSpace(pc.APIKey) != ""

	if hasOAuth || hasAPIKey {
		return nil // Already authenticated.
	}

	_, supportsOAuth := authProviders[providerName]
	if supportsOAuth {
		authChoice := "oauth"
		err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("How to authenticate with " + providerName + "?").
				Options(
					huh.NewOption("Login with OAuth (use your existing account)", "oauth"),
					huh.NewOption("Enter API key manually", "apikey"),
				).
				Value(&authChoice),
		)).Run()
		if err != nil {
			return err
		}
		if authChoice == "oauth" {
			return runOAuthLogin(providerName)
		}
	}

	// API key input.
	keyURL := providerURLs[providerName]
	var apiKey string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Enter your " + providerName + " API key").
			Description("Create one at " + keyURL).
			EchoMode(huh.EchoModePassword).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("API key is required")
				}
				return nil
			}).
			Value(&apiKey),
	)).Run()
	if err != nil {
		return err
	}
	existing.EnsureProviderConfigFor(providerName).APIKey = strings.TrimSpace(apiKey)
	return nil
}

// copyEmbeddedDir recursively copies an embedded directory tree to dest,
// always overwriting existing files.
func copyEmbeddedDir(embeddedRoot, dest string) error {
	entries, err := templateFS.ReadDir(embeddedRoot)
	if err != nil {
		return fmt.Errorf("read embedded dir %s: %w", embeddedRoot, err)
	}
	for _, entry := range entries {
		srcPath := embeddedRoot + "/" + entry.Name()
		dstPath := filepath.Join(dest, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return err
			}
			if err := copyEmbeddedDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := templateFS.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("read embedded file %s: %w", srcPath, err)
		}
		if err := os.WriteFile(dstPath, data, 0644); err != nil {
			return err
		}
	}
	return nil
}
