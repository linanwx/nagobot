package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/session"
	"github.com/spf13/cobra"
)

var setSummaryCmd = &cobra.Command{
	Use:     "set-summary <key> <summary>",
	Short:   "Set the summary for a session",
	GroupID: "internal",
	Args:    cobra.ExactArgs(2),
	RunE:    runSetSummary,
}

func init() {
	rootCmd.AddCommand(setSummaryCmd)
}

func runSetSummary(_ *cobra.Command, args []string) error {
	key := strings.TrimSpace(args[0])
	summary := strings.TrimSpace(args[1])
	if key == "" {
		return fmt.Errorf("session key is required")
	}
	if summary == "" {
		return fmt.Errorf("summary is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	summaryPath := filepath.Join(workspace, "system", "sessions_summary.json")
	summaries := loadSummariesFile(summaryPath)
	if summaries == nil {
		summaries = make(map[string]summaryEntry)
	}

	summaries[key] = summaryEntry{
		Summary:   summary,
		SummaryAt: time.Now(),
	}

	// Cleanup entries whose session hasn't been active in 7+ days.
	sessionsDir := filepath.Join(workspace, "sessions")
	cutoff := time.Now().AddDate(0, 0, -7)
	var cleaned []string
	for k := range summaries {
		sessionPath := filepath.Join(sessionsDir, filepath.FromSlash(strings.ReplaceAll(k, ":", "/")), session.SessionFileName)
		ts, readErr := session.ReadUpdatedAt(sessionPath)
		if readErr != nil || ts.IsZero() || ts.Before(cutoff) {
			cleaned = append(cleaned, k)
			delete(summaries, k)
		}
	}

	if err := os.MkdirAll(filepath.Dir(summaryPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}
	// Atomic write: temp file + rename to avoid corruption on crash.
	tmp := summaryPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := os.Rename(tmp, summaryPath); err != nil {
		return fmt.Errorf("failed to rename: %w", err)
	}

	fmt.Printf("Summary saved for %q.\n", key)
	if len(cleaned) > 0 {
		fmt.Printf("Cleaned %d stale entries (inactive >7 days): %s\n", len(cleaned), strings.Join(cleaned, ", "))
	}
	return nil
}
