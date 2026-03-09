package cmd

import (
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

const defaultSkillHubURL = "https://clawhub.ai"

var hubCmd = &cobra.Command{
	Use:     "hub",
	Short:   "Show or configure the skill hub",
	GroupID: "internal",
	RunE:    runHubShow,
}

var hubSetCmd = &cobra.Command{
	Use:   "set <url>",
	Short: "Set the skill hub URL",
	Args:  cobra.ExactArgs(1),
	RunE:  runHubSet,
}

var hubResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset skill hub to default (ClawHub)",
	RunE:  runHubReset,
}

func init() {
	hubCmd.AddCommand(hubSetCmd, hubResetCmd)
	rootCmd.AddCommand(hubCmd)
}

func runHubShow(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// applyDefaults() ensures URL is never empty.
	fmt.Printf("Skill hub: %s\n", cfg.SkillHub.URL)
	return nil
}

func runHubSet(cmd *cobra.Command, args []string) error {
	url := strings.TrimSpace(args[0])

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	cfg.SkillHub.URL = url
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("cannot save config: %w", err)
	}

	fmt.Printf("Skill hub set to: %s\n", url)
	return nil
}

func runHubReset(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	cfg.SkillHub.URL = defaultSkillHubURL
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("cannot save config: %w", err)
	}

	fmt.Printf("Skill hub reset to: %s\n", defaultSkillHubURL)
	return nil
}
