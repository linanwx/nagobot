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

var (
	listSessionsDays        int
	listSessionsUserOnly    bool
	listSessionsChangedOnly bool
	listSessionsFields      string
)

var listSessionsCmd = &cobra.Command{
	Use:     "list-sessions",
	Short:   "List active sessions with summary status",
	GroupID: "internal",
	RunE:    runListSessions,
}

func init() {
	listSessionsCmd.Flags().IntVar(&listSessionsDays, "days", 2, "Only show sessions active within N days")
	listSessionsCmd.Flags().BoolVar(&listSessionsUserOnly, "user-only", false, "Exclude cron:*, :threads:, and sessions without user activity")
	listSessionsCmd.Flags().BoolVar(&listSessionsChangedOnly, "changed-only", false, "Exclude sessions with changed_since_summary=false or message_count=0")
	listSessionsCmd.Flags().StringVar(&listSessionsFields, "fields", "", "Comma-separated list of fields to include (e.g. key,is_running,has_heartbeat)")
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
	IsRunning           bool    `json:"is_running"`
	HasHeartbeat        bool    `json:"has_heartbeat"`
	LastUserActiveAt    *string `json:"last_user_active_at"`
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

	opts := listSessionsOpts{
		Days:        listSessionsDays,
		UserOnly:    listSessionsUserOnly,
		ChangedOnly: listSessionsChangedOnly,
	}

	// Try RPC to running serve process first.
	result, err := rpcCall("sessions.list", opts)
	if err == nil {
		var output listSessionsOutput
		if jsonErr := json.Unmarshal(result, &output); jsonErr == nil {
			applyPostFilters(&output, opts)
			return encodeSessionsOutput(&output)
		}
	}

	// Fallback to file scanning.
	output, err := collectSessions(cfg, opts)
	if err != nil {
		return err
	}
	return encodeSessionsOutput(output)
}

// listSessionsOpts holds query parameters for list-sessions.
type listSessionsOpts struct {
	Days        int  `json:"days"`
	UserOnly    bool `json:"user_only,omitempty"`
	ChangedOnly bool `json:"changed_only,omitempty"`
}

// encodeSessionsOutput writes the output as JSON, applying --fields filtering if set.
func encodeSessionsOutput(output *listSessionsOutput) error {
	if listSessionsFields == "" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	}

	fields := make(map[string]bool)
	for _, f := range strings.Split(listSessionsFields, ",") {
		fields[strings.TrimSpace(f)] = true
	}

	// Re-encode each session entry through map to filter fields.
	var filtered []map[string]any
	for _, s := range output.Sessions {
		raw, _ := json.Marshal(s)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		out := make(map[string]any, len(fields))
		for k, v := range m {
			if fields[k] {
				out[k] = v
			}
		}
		filtered = append(filtered, out)
	}
	if filtered == nil {
		filtered = []map[string]any{}
	}

	wrapper := map[string]any{
		"sessions":       filtered,
		"filter":         output.Filter,
		"total_sessions": output.TotalSessions,
		"shown_sessions": len(filtered),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(wrapper)
}

// collectSessions scans session files on disk and returns a summary.
// IsRunning defaults to false (only populated via RPC from a running serve).
func collectSessions(cfg *config.Config, opts listSessionsOpts) (*listSessionsOutput, error) {
	days := opts.Days
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

		// Early exit for cron/threads (avoids expensive file reads below).
		if opts.UserOnly && (strings.HasPrefix(key, "cron:") || strings.Contains(key, ":threads:")) {
			return nil
		}

		updatedAt := s.UpdatedAt
		if updatedAt.IsZero() || updatedAt.Before(cutoff) {
			return nil
		}

		tz := cfg.SessionTimezone(key)
		tzSource := "machine_default"
		if cfg.Channels != nil && cfg.Channels.SessionTimezones[key] != "" {
			tzSource = "configured"
		}

		// Check for non-empty heartbeat file in the session directory.
		sessionDir := filepath.Dir(path)
		hasHeartbeat := false
		if data, readErr := os.ReadFile(filepath.Join(sessionDir, "heartbeat.md")); readErr == nil {
			hasHeartbeat = len(strings.TrimSpace(string(data))) > 0
		}

		// Scan backwards for the last real-user message.
		var lastUserActiveAt *string
		for i := len(s.Messages) - 1; i >= 0; i-- {
			if isRealUserSource(s.Messages[i].Source) && !s.Messages[i].Timestamp.IsZero() {
				t := s.Messages[i].Timestamp.Format(time.RFC3339)
				lastUserActiveAt = &t
				break
			}
		}

		if opts.UserOnly && lastUserActiveAt == nil {
			return nil
		}

		entry := sessionEntry{
			Key:              key,
			Timezone:         tz,
			TimezoneSource:   tzSource,
			UpdatedAt:        updatedAt.Format(time.RFC3339),
			MessageCount:     len(s.Messages),
			HasHeartbeat:     hasHeartbeat,
			LastUserActiveAt: lastUserActiveAt,
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

		if opts.ChangedOnly && (!entry.ChangedSinceSummary || entry.MessageCount == 0) {
			return nil
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

// applyPostFilters applies client-side filters (user-only, changed-only) to RPC results.
// Mirrors the filtering in collectSessions for the RPC path.
func applyPostFilters(output *listSessionsOutput, opts listSessionsOpts) {
	if !opts.UserOnly && !opts.ChangedOnly {
		return
	}
	filtered := output.Sessions[:0]
	for _, s := range output.Sessions {
		if opts.UserOnly && isExcludedByUserOnly(s.Key, s.LastUserActiveAt) {
			continue
		}
		if opts.ChangedOnly && (!s.ChangedSinceSummary || s.MessageCount == 0) {
			continue
		}
		filtered = append(filtered, s)
	}
	output.Sessions = filtered
	output.ShownSessions = len(filtered)
}

// isExcludedByUserOnly returns true if the session should be excluded by --user-only.
func isExcludedByUserOnly(key string, lastUserActiveAt *string) bool {
	return strings.HasPrefix(key, "cron:") || strings.Contains(key, ":threads:") || lastUserActiveAt == nil
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

// isRealUserSource returns true if the source is a real user channel.
func isRealUserSource(source string) bool {
	return msg.IsUserVisibleSource(msg.WakeSource(source))
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
