package cmd

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

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
	"deepseek":        "https://platform.deepseek.com",
	"openrouter":      "https://openrouter.ai/keys",
	"anthropic":       "https://console.anthropic.com",
	"moonshot-cn":     "https://platform.moonshot.cn",
	"moonshot-global": "https://platform.moonshot.ai",
}

func runOnboard(_ *cobra.Command, _ []string) error {
	configPath, err := config.ConfigPath()
	if err != nil {
		return err
	}

	// Load existing config as defaults, or start fresh.
	existing, _ := config.Load()
	defaults := loadOnboardDefaults(existing)

	// --- interactive wizard ---

	var (
		selectedProvider = defaults.provider
		selectedModel    = defaults.model
		apiKey           = defaults.apiKey
		configureTG      bool
	)

	// Step 1: select provider
	providerOptions := buildProviderOptions()
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose your LLM provider").
				Description("nagobot supports multiple LLM providers. Choose one to get started.").
				Options(providerOptions...).
				Value(&selectedProvider),
		),
	).Run()
	if err != nil {
		return err
	}

	// Step 2: select model (dynamic based on provider)
	// Reset model if provider changed.
	if selectedProvider != defaults.provider {
		selectedModel = ""
	}
	modelOptions := buildModelOptions(selectedProvider)
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose model for "+selectedProvider).
				Description("Only whitelisted models are supported. The first option is the recommended default.").
				Options(modelOptions...).
				Value(&selectedModel),
		),
	).Run()
	if err != nil {
		return err
	}

	// Step 3: API key
	// Reset key if provider changed.
	if selectedProvider != defaults.provider {
		apiKey = ""
	}
	keyURL := providerURLs[selectedProvider]
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Enter your "+selectedProvider+" API key").
				Description("Create one at "+keyURL).
				EchoMode(huh.EchoModePassword).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("API key is required")
					}
					return nil
				}).
				Value(&apiKey),
		),
	).Run()
	if err != nil {
		return err
	}

	// Step 4: optional Telegram
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

	cfg := config.DefaultConfig()
	cfg.SetProvider(selectedProvider)
	cfg.SetModelType(selectedModel)
	cfg.SetProviderAPIKey(strings.TrimSpace(apiKey))

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
	fmt.Println("  Provider:", selectedProvider)
	fmt.Println("  Model:", selectedModel)
	fmt.Println()
	fmt.Println("Run 'nagobot serve' to start.")
	return nil
}

// onboardDefaults holds pre-filled values from existing config.
type onboardDefaults struct {
	provider     string
	model        string
	apiKey       string
	tgToken      string
	tgAdminID    string
	tgAllowedIDs string
}

func loadOnboardDefaults(cfg *config.Config) onboardDefaults {
	if cfg == nil {
		return onboardDefaults{}
	}
	apiKey, _ := cfg.GetAPIKey()
	return onboardDefaults{
		provider:      cfg.GetProvider(),
		model:         cfg.GetModelType(),
		apiKey:        apiKey,
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

	// USER.md is the only flat template; skip if it already exists.
	if err := writeTemplate(workspace, "USER.md", "USER.md", false); err != nil {
		return err
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
