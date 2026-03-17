package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

const heartbeatActiveThreshold = 5 * time.Minute

var listHeartbeatCmd = &cobra.Command{
	Use:     "list-heartbeat",
	Short:   "List sessions eligible for heartbeat operations",
	GroupID: "internal",
	RunE:    runListHeartbeat,
}

func init() {
	rootCmd.AddCommand(listHeartbeatCmd)
}

type heartbeatEntry struct {
	Key                 string  `json:"key"`
	Timezone            string  `json:"timezone,omitempty"`
	TimezoneSource      string  `json:"timezone_source,omitempty"`
	UpdatedAt           string  `json:"updated_at"`
	MessageCount        int     `json:"message_count"`
	Summary             string  `json:"summary"`
	SummaryAt           string  `json:"summary_at,omitempty"`
	ChangedSinceSummary bool    `json:"changed_since_summary"`
	HasHeartbeat        bool    `json:"has_heartbeat"`
	HeartbeatContent    string  `json:"heartbeat_content,omitempty"`
	LastUserActiveAt    *string `json:"last_user_active_at"`
	LastReflection      *string `json:"last_reflection,omitempty"`
}

type listHeartbeatOutput struct {
	Sessions []heartbeatEntry `json:"sessions"`
}

func runListHeartbeat(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	opts := listSessionsOpts{Days: 2, UserOnly: true}

	// Try RPC first, fallback to file scanning.
	var raw *listSessionsOutput
	result, err := rpcCall("sessions.list", opts)
	if err == nil {
		var output listSessionsOutput
		if jsonErr := json.Unmarshal(result, &output); jsonErr == nil {
			applyPostFilters(&output, opts)
			raw = &output
		}
	}
	if raw == nil {
		raw, err = collectSessions(cfg, opts)
		if err != nil {
			return err
		}
	}

	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}
	sessionsDir, err := cfg.SessionsDir()
	if err != nil {
		return fmt.Errorf("failed to get sessions dir: %w", err)
	}

	hbState := loadHeartbeatState(filepath.Join(workspace, "system", "heartbeat-state.json"))
	now := time.Now()

	var eligible []heartbeatEntry
	for _, s := range raw.Sessions {
		// Deterministic filter: skip running sessions.
		if s.IsRunning {
			continue
		}
		// Deterministic filter: skip sessions active within 5 minutes.
		if s.LastUserActiveAt != nil {
			if t, parseErr := time.Parse(time.RFC3339, *s.LastUserActiveAt); parseErr == nil {
				if now.Sub(t) < heartbeatActiveThreshold {
					continue
				}
			}
		}

		entry := heartbeatEntry{
			Key:                 s.Key,
			Timezone:            s.Timezone,
			TimezoneSource:      s.TimezoneSource,
			UpdatedAt:           s.UpdatedAt,
			MessageCount:        s.MessageCount,
			Summary:             s.Summary,
			SummaryAt:           s.SummaryAt,
			ChangedSinceSummary: s.ChangedSinceSummary,
			HasHeartbeat:        s.HasHeartbeat,
			LastUserActiveAt:    s.LastUserActiveAt,
		}

		// Read heartbeat.md content.
		sessionDir := sessionKeyToDir(sessionsDir, s.Key)
		if data, readErr := os.ReadFile(filepath.Join(sessionDir, "heartbeat.md")); readErr == nil {
			if content := strings.TrimSpace(string(data)); content != "" {
				entry.HeartbeatContent = content
			}
		}

		// Enrich with last_reflection from heartbeat-state.json.
		if lr, ok := hbState["last_reflection"][s.Key]; ok {
			entry.LastReflection = &lr
		}

		eligible = append(eligible, entry)
	}
	if eligible == nil {
		eligible = []heartbeatEntry{}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(listHeartbeatOutput{Sessions: eligible})
}

// sessionKeyToDir converts a session key back to its directory path.
// telegram:12345 -> {sessionsDir}/telegram/12345
func sessionKeyToDir(sessionsDir, key string) string {
	parts := strings.Split(key, ":")
	return filepath.Join(append([]string{sessionsDir}, parts...)...)
}

// loadHeartbeatState reads system/heartbeat-state.json.
func loadHeartbeatState(path string) map[string]map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]map[string]string{}
	}
	var m map[string]map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]map[string]string{}
	}
	return m
}
