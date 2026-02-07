package cmd

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/internal/runtimecfg"
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

func runOnboard(cmd *cobra.Command, args []string) error {
	// Check if config already exists
	configPath, err := config.ConfigPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(configPath); err == nil {
		fmt.Println("Config already exists at:", configPath)
		fmt.Println("To reconfigure, edit the file directly or delete it first.")
		return nil
	}

	// Create default config
	cfg := config.DefaultConfig()

	// Create config directory
	configDir, _ := config.ConfigDir()
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create workspace directory
	if err := cfg.EnsureWorkspace(); err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}

	// Create workspace bootstrap files from embedded templates
	workspace, _ := cfg.WorkspacePath()
	if err := createBootstrapFiles(workspace); err != nil {
		return fmt.Errorf("failed to create bootstrap files: %w", err)
	}

	// Save config
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println("nagobot initialized successfully!")
	fmt.Println()
	fmt.Println("Config file:", configPath)
	fmt.Println("Workspace:", workspace)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Edit", configPath, "and add your API key")
	fmt.Println("  2. Run 'nagobot agent -m \"hello\"' to test")
	fmt.Println()
	fmt.Println("Default configuration:")
	fmt.Println("  Provider: openrouter")
	fmt.Println("  Model: moonshotai/kimi-k2.5")
	fmt.Println()
	fmt.Println("Get your OpenRouter API key at: https://openrouter.ai/keys")

	return nil
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
	// Create default workspace directories first.
	for _, dir := range []string{
		"agents",
		runtimecfg.WorkspaceSkillsDirName,
		filepath.Join(runtimecfg.WorkspaceSessionsDirName, "main"),
		filepath.Join(runtimecfg.WorkspaceSessionsDirName, "cron"),
	} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0755); err != nil {
			return err
		}
	}

	// Write top-level template files
	templates := []struct{ src, dst string }{
		{"SOUL.md", "SOUL.md"},
		{"USER.md", "USER.md"},
		{"GENERAL.md", filepath.Join("agents", "GENERAL.md")},
		{"EXPLAIN_RUNTIME.md", filepath.Join(runtimecfg.WorkspaceSkillsDirName, "EXPLAIN_RUNTIME.md")},
		{"COMPRESS_CONTEXT.md", filepath.Join(runtimecfg.WorkspaceSkillsDirName, "COMPRESS_CONTEXT.md")},
	}
	for _, t := range templates {
		if err := writeTemplate(workspace, t.src, t.dst); err != nil {
			return err
		}
	}

	return nil
}
