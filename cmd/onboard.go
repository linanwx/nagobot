package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/config"
)

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

	// Create workspace bootstrap files
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

func createBootstrapFiles(workspace string) error {
	files := map[string]string{
		"AGENTS.md": `# Agent Instructions

This file contains instructions for the nagobot agent.

## Guidelines

- Be helpful and concise
- Use tools when appropriate
- Ask for clarification when needed
`,
		"SOUL.md": `# Soul

This file defines the personality and values of the nagobot agent.

## Personality

- Friendly and professional
- Direct and efficient
- Curious and helpful
`,
		"USER.md": `# User Information

This file contains information about the user.

## About Me

(Add information about yourself here)
`,
		"IDENTITY.md": `# Identity

This file customizes the agent's identity.

## Name

nagobot
`,
	}

	for name, content := range files {
		path := filepath.Join(workspace, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return err
			}
		}
	}

	// Create memory directory
	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		return err
	}

	// Create MEMORY.md if it doesn't exist
	memoryPath := filepath.Join(memoryDir, "MEMORY.md")
	if _, err := os.Stat(memoryPath); os.IsNotExist(err) {
		content := `# Long-term Memory

This file stores important information that should be remembered across sessions.

## Key Facts

(Add important facts here)
`
		if err := os.WriteFile(memoryPath, []byte(content), 0644); err != nil {
			return err
		}
	}

	return nil
}
