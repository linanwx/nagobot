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
	Long:  `Create the nagobot configuration directory and default config file.`,
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
	if _, err := os.Stat(configPath); err == nil {
		fmt.Println("Config already exists at:", configPath)
		fmt.Println("To reconfigure, edit the file directly or delete it first.")
		return nil
	}

	// --- interactive wizard ---

	var (
		selectedProvider string
		selectedModel    string
		apiKey           string
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

	var tgToken, tgAdminID, tgAllowedIDs string
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

	configDir, _ := config.ConfigDir()
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	if err := cfg.EnsureWorkspace(); err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}
	workspace, _ := cfg.WorkspacePath()
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

// writeTemplate writes an embedded template file to the workspace,
// skipping if the file already exists.
func writeTemplate(workspace, templateName, destName string) error {
	destPath := filepath.Join(workspace, destName)
	if _, err := os.Stat(destPath); err == nil {
		return nil // already exists, don't overwrite
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
		"agents",
		"docs",
		skillsDir,
		filepath.Join(sessionsDir, "main"),
		filepath.Join(sessionsDir, "cron"),
	} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0755); err != nil {
			return err
		}
	}

	templates := []struct{ src, dst string }{
		{"SOUL.md", "SOUL.md"},
		{"USER.md", "USER.md"},
		{"GENERAL.md", filepath.Join("agents", "GENERAL.md")},
		{"EXPLAIN_RUNTIME.md", filepath.Join(skillsDir, "EXPLAIN_RUNTIME.md")},
		{"COMPRESS_CONTEXT.md", filepath.Join(skillsDir, "COMPRESS_CONTEXT.md")},
		{"MANAGE_CRON.md", filepath.Join(skillsDir, "MANAGE_CRON.md")},
	}
	for _, t := range templates {
		if err := writeTemplate(workspace, t.src, t.dst); err != nil {
			return err
		}
	}

	return nil
}
