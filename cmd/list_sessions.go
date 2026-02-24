package cmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

var listSessionsDays int

var listSessionsCmd = &cobra.Command{
	Use:     "list-sessions",
	Short:   "List active sessions with summary status",
	GroupID: "internal",
	RunE:    runListSessions,
}

func init() {
	listSessionsCmd.Flags().IntVar(&listSessionsDays, "days", 2, "Only show sessions active within N days")
	rootCmd.AddCommand(listSessionsCmd)
}

type sessionEntry struct {
	Key                 string `json:"key"`
	Timezone            string `json:"timezone,omitempty"`
	UpdatedAt           string `json:"updated_at"`
	MessageCount        int    `json:"message_count"`
	Summary             string `json:"summary"`
	SummaryAt           string `json:"summary_at,omitempty"`
	ChangedSinceSummary bool   `json:"changed_since_summary"`
}

type listSessionsOutput struct {
	Sessions     []sessionEntry `json:"sessions"`
	Filter       string         `json:"filter"`
	TotalSessions int           `json:"total_sessions"`
	ShownSessions int           `json:"shown_sessions"`
}

func runListSessions(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	sessionsDir := filepath.Join(workspace, "sessions")
	summaries := loadSummariesFile(filepath.Join(workspace, "system", "sessions_summary.json"))
	cutoff := time.Now().AddDate(0, 0, -listSessionsDays)

	type rawSession struct {
		Key       string `json:"key"`
		UpdatedAt time.Time `json:"updated_at"`
		Messages  []json.RawMessage `json:"messages"`
	}

	var all []sessionEntry
	total := 0

	_ = filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || d.Name() != "session.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var raw rawSession
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil
		}
		// Derive key from path if not stored.
		key := raw.Key
		if key == "" {
			key = deriveSessionKey(sessionsDir, path)
		}
		total++

		if raw.UpdatedAt.Before(cutoff) {
			return nil
		}

		entry := sessionEntry{
			Key:          key,
			Timezone:     cfg.SessionTimezone(key),
			UpdatedAt:    raw.UpdatedAt.Format(time.RFC3339),
			MessageCount: len(raw.Messages),
		}

		if s, ok := summaries[key]; ok {
			entry.Summary = s.Summary
			if !s.SummaryAt.IsZero() {
				entry.SummaryAt = s.SummaryAt.Format(time.RFC3339)
				entry.ChangedSinceSummary = raw.UpdatedAt.After(s.SummaryAt)
			} else {
				entry.ChangedSinceSummary = true
			}
		} else {
			entry.ChangedSinceSummary = true
		}

		all = append(all, entry)
		return nil
	})

	output := listSessionsOutput{
		Sessions:      all,
		Filter:        fmt.Sprintf("showing sessions active in last %d days (use --days N to adjust)", listSessionsDays),
		TotalSessions: total,
		ShownSessions: len(all),
	}
	if output.Sessions == nil {
		output.Sessions = []sessionEntry{}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

// deriveSessionKey reconstructs a session key from filesystem path.
// sessions/telegram/12345/session.json → telegram:12345
func deriveSessionKey(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	// Remove trailing /session.json
	rel = filepath.Dir(rel)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return strings.Join(parts, ":")
}

// summaryEntry is a single entry in sessions_summary.json.
type summaryEntry struct {
	Summary   string    `json:"summary"`
	SummaryAt time.Time `json:"summary_at"`
}

// loadSummariesFile reads system/sessions_summary.json.
func loadSummariesFile(path string) map[string]summaryEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]summaryEntry
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}
