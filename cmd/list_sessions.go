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
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread/msg"
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
	TimezoneSource      string `json:"timezone_source,omitempty"` // "configured" or "machine_default"
	UpdatedAt           string `json:"updated_at"`
	MessageCount        int    `json:"message_count"`
	Summary             string `json:"summary"`
	SummaryAt           string `json:"summary_at,omitempty"`
	ChangedSinceSummary bool   `json:"changed_since_summary"`
	IsRunning           bool   `json:"is_running"`
	HasHeartbeat        bool   `json:"has_heartbeat"`
}

type listSessionsOutput struct {
	Sessions      []sessionEntry `json:"sessions"`
	Filter        string         `json:"filter"`
	TotalSessions int            `json:"total_sessions"`
	ShownSessions int            `json:"shown_sessions"`
}

func runListSessions(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Try RPC to running serve process first.
	result, err := rpcCall("sessions.list", map[string]int{"days": listSessionsDays})
	if err == nil {
		var output listSessionsOutput
		if jsonErr := json.Unmarshal(result, &output); jsonErr == nil {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(output)
		}
	}

	// Fallback to file scanning.
	output, err := collectSessions(cfg, listSessionsDays)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

// collectSessions scans session files on disk and returns a summary.
// IsRunning defaults to false (only populated via RPC from a running serve).
func collectSessions(cfg *config.Config, days int) (*listSessionsOutput, error) {
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}

	sessionsDir, err := cfg.SessionsDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions dir: %w", err)
	}
	summaries := loadSummariesFile(filepath.Join(workspace, "system", "sessions_summary.json"))
	cutoff := time.Now().AddDate(0, 0, -days)

	var all []sessionEntry
	total := 0

	_ = filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || d.Name() != session.SessionFileName {
			return nil
		}
		s, err := session.ReadFile(path)
		if err != nil {
			return nil
		}
		key := deriveSessionKey(sessionsDir, path)
		total++

		updatedAt := s.UpdatedAt
		if updatedAt.IsZero() {
			// Fallback to file mtime for empty sessions.
			if fi, statErr := os.Stat(path); statErr == nil {
				updatedAt = fi.ModTime()
			}
		}
		if updatedAt.Before(cutoff) {
			return nil
		}

		tz := cfg.SessionTimezone(key)
		tzSource := "machine_default"
		if cfg.Channels != nil && cfg.Channels.SessionTimezones[key] != "" {
			tzSource = "configured"
		}

		// Check for heartbeat file in the session directory.
		sessionDir := filepath.Dir(path)
		hasHeartbeat := false
		if _, statErr := os.Stat(filepath.Join(sessionDir, "heartbeat.md")); statErr == nil {
			hasHeartbeat = true
		}

		entry := sessionEntry{
			Key:            key,
			Timezone:       tz,
			TimezoneSource: tzSource,
			UpdatedAt:      updatedAt.Format(time.RFC3339),
			MessageCount:   len(s.Messages),
			HasHeartbeat:   hasHeartbeat,
		}

		if s, ok := summaries[key]; ok {
			entry.Summary = s.Summary
			if !s.SummaryAt.IsZero() {
				entry.SummaryAt = s.SummaryAt.Format(time.RFC3339)
				entry.ChangedSinceSummary = updatedAt.After(s.SummaryAt)
			} else {
				entry.ChangedSinceSummary = true
			}
		} else {
			entry.ChangedSinceSummary = true
		}

		all = append(all, entry)
		return nil
	})

	output := &listSessionsOutput{
		Sessions:      all,
		Filter:        fmt.Sprintf("showing sessions active in last %d days (use --days N to adjust)", days),
		TotalSessions: total,
		ShownSessions: len(all),
	}
	if output.Sessions == nil {
		output.Sessions = []sessionEntry{}
	}

	return output, nil
}

// enrichWithThreads fills IsRunning from live thread state.
func enrichWithThreads(output *listSessionsOutput, threads []msg.ThreadInfo) {
	running := make(map[string]bool, len(threads))
	for _, t := range threads {
		if t.State == "running" || t.State == "pending" {
			running[t.SessionKey] = true
		}
	}
	for i := range output.Sessions {
		if running[output.Sessions[i].Key] {
			output.Sessions[i].IsRunning = true
		}
	}
}

// deriveSessionKey reconstructs a session key from filesystem path.
// sessions/telegram/12345/session.jsonl -> telegram:12345
func deriveSessionKey(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	// Remove trailing /session.jsonl
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
