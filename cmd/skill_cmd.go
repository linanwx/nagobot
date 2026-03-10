package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/skills"
	"github.com/spf13/cobra"
)

var skillCmd = &cobra.Command{
	Use:     "skill",
	Short:   "Manage skills (search, install, remove, list, update)",
	GroupID: "internal",
}

var skillSearchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search for skills on ClawHub",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSkillSearch,
}

var skillInstallCmd = &cobra.Command{
	Use:   "install <slug>",
	Short: "Install a skill from ClawHub",
	Args:  cobra.ExactArgs(1),
	RunE:  runSkillInstall,
}

var skillRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an installed skill",
	Args:  cobra.ExactArgs(1),
	RunE:  runSkillRemove,
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed skills",
	RunE:  runSkillList,
}

var skillUpdateCmd = &cobra.Command{
	Use:   "update [name]",
	Short: "Update hub-installed skill(s)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSkillUpdate,
}

func init() {
	skillInstallCmd.Flags().Bool("force", false, "Force install even if skill already exists")

	skillCmd.AddCommand(skillSearchCmd, skillInstallCmd, skillRemoveCmd, skillListCmd, skillUpdateCmd)
	rootCmd.AddCommand(skillCmd)
}

func hubClient(cfg *config.Config) *skills.HubClient {
	return skills.NewHubClient(cfg.SkillHub.URL)
}

func runSkillSearch(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	query := ""
	if len(args) > 0 {
		query = args[0]
	}

	client := hubClient(cfg)
	fmt.Printf("Searching %s for %q...\n\n", client.BaseURL, query)

	results, err := client.Search(query, 20)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Println("No skills found.")
		return nil
	}

	for _, r := range results {
		owner := ""
		if r.Owner != "" {
			owner = r.Owner + "/"
		}
		fmt.Printf("  %s%s\n", owner, r.Slug)
		if r.Description != "" {
			fmt.Printf("    %s\n", r.Description)
		}
	}
	fmt.Printf("\n%d skill(s) found. Install with: nagobot skill install <slug>\n", len(results))
	return nil
}

func runSkillInstall(cmd *cobra.Command, args []string) error {
	slug := args[0]
	force, _ := cmd.Flags().GetBool("force")

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return err
	}
	skillsDir := filepath.Join(workspace, "skills")

	// Check existing.
	installed, err := skills.LoadInstalled(workspace)
	if err != nil {
		return fmt.Errorf("cannot load tracking: %w", err)
	}

	existingDir := filepath.Join(skillsDir, slug)
	if _, statErr := os.Stat(existingDir); statErr == nil && !force {
		if meta, tracked := installed.IsTracked(slug); tracked {
			fmt.Printf("Skill %q already installed (from %s). Use --force to re-install.\n", slug, meta.Hub)
		} else {
			fmt.Printf("Skill %q already exists locally. Use --force to overwrite.\n", slug)
		}
		return nil
	}

	client := hubClient(cfg)
	fmt.Printf("Installing %s from %s...\n", slug, client.BaseURL)

	if err := client.Install(slug, skillsDir); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}

	installed.Track(slug, client.BaseURL)
	if err := installed.Save(workspace); err != nil {
		return fmt.Errorf("cannot save tracking: %w", err)
	}

	fmt.Printf("Installed %s.\n", slug)
	return nil
}

func runSkillRemove(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return err
	}

	skillDir := filepath.Join(workspace, "skills", name)
	if _, statErr := os.Stat(skillDir); os.IsNotExist(statErr) {
		return fmt.Errorf("skill %q not found", name)
	}

	if err := os.RemoveAll(skillDir); err != nil {
		return fmt.Errorf("cannot remove: %w", err)
	}

	installed, err := skills.LoadInstalled(workspace)
	if err == nil {
		installed.Untrack(name)
		_ = installed.Save(workspace)
	}

	fmt.Printf("Removed %s.\n", name)
	return nil
}

func runSkillList(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return err
	}

	builtinSkillsDir := filepath.Join(workspace, "skills-builtin")
	userSkillsDir := filepath.Join(workspace, "skills")
	installed, _ := skills.LoadInstalled(workspace)

	// Collect skills from both directories. Built-in overrides stale user copies.
	type skillEntry struct {
		name   string
		source string
	}
	seen := make(map[string]skillEntry)

	// User-installed skills first.
	if entries, err := os.ReadDir(userSkillsDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if skills.FindSkillFile(filepath.Join(userSkillsDir, entry.Name())) == "" {
				continue
			}
			source := "user"
			if installed != nil {
				if meta, tracked := installed.IsTracked(entry.Name()); tracked {
					source = "hub:" + meta.Hub
				}
			}
			seen[entry.Name()] = skillEntry{name: entry.Name(), source: source}
		}
	}

	// Built-in skills (override stale user copies on name conflict).
	if entries, err := os.ReadDir(builtinSkillsDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if skills.FindSkillFile(filepath.Join(builtinSkillsDir, entry.Name())) == "" {
				continue
			}
			seen[entry.Name()] = skillEntry{name: entry.Name(), source: "builtin"}
		}
	}

	if len(seen) == 0 {
		fmt.Println("No skills installed.")
		return nil
	}

	// Sort and print.
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Printf("  %-30s [%s]\n", name, seen[name].source)
	}
	fmt.Printf("\n%d skill(s) total.\n", len(seen))
	return nil
}

func runSkillUpdate(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return err
	}

	installed, err := skills.LoadInstalled(workspace)
	if err != nil {
		return fmt.Errorf("cannot load tracking: %w", err)
	}

	skillsDir := filepath.Join(workspace, "skills")

	var targets []string
	if len(args) > 0 {
		name := args[0]
		if _, tracked := installed.IsTracked(name); !tracked {
			return fmt.Errorf("skill %q is not installed from a hub", name)
		}
		targets = []string{name}
	} else {
		for name := range installed.Skills {
			targets = append(targets, name)
		}
	}

	if len(targets) == 0 {
		fmt.Println("No hub-installed skills to update.")
		return nil
	}

	client := hubClient(cfg)
	var updated int
	for _, name := range targets {
		if err := client.Install(name, skillsDir); err != nil {
			fmt.Printf("  %s: failed: %v\n", name, err)
			continue
		}
		installed.Track(name, client.BaseURL)
		fmt.Printf("  %s: updated\n", name)
		updated++
	}

	if err := installed.Save(workspace); err != nil {
		return fmt.Errorf("cannot save tracking: %w", err)
	}

	fmt.Printf("\n%d/%d skill(s) updated.\n", updated, len(targets))
	return nil
}
