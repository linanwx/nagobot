package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

var heartbeatCmd = &cobra.Command{
	Use:     "heartbeat",
	Short:   "Heartbeat operations for user sessions",
	GroupID: "internal",
}

var heartbeatPostponeCmd = &cobra.Command{
	Use:   "postpone <session-key> <duration>",
	Short: "Postpone heartbeat for a session (e.g., 4h, 30m)",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		key := args[0]
		d, err := time.ParseDuration(args[1])
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", args[1], err)
		}
		if d <= 0 {
			return fmt.Errorf("duration must be positive")
		}
		if d > 24*time.Hour {
			return fmt.Errorf("duration must not exceed 24h")
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		workspace, err := cfg.WorkspacePath()
		if err != nil {
			return fmt.Errorf("workspace: %w", err)
		}

		postponePath := filepath.Join(workspace, "system", "heartbeat-postpone.json")

		postpone := make(map[string]string)
		if data, err := os.ReadFile(postponePath); err == nil {
			_ = json.Unmarshal(data, &postpone)
		}

		until := time.Now().Add(d).UTC()
		postpone[key] = until.Format(time.RFC3339)

		now := time.Now()
		for k, v := range postpone {
			if t, err := time.Parse(time.RFC3339, v); err == nil && now.After(t) {
				delete(postpone, k)
			}
		}

		data, err := json.MarshalIndent(postpone, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(postponePath), 0755); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}
		if err := os.WriteFile(postponePath, data, 0644); err != nil {
			return fmt.Errorf("write: %w", err)
		}

		fmt.Printf("Heartbeat postponed for session %q until %s (%s from now).\n", key, until.Local().Format("15:04"), d)
		return nil
	},
}

var heartbeatStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show next heartbeat pulse time for all eligible sessions",
	RunE: func(_ *cobra.Command, _ []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		workspace, err := cfg.WorkspacePath()
		if err != nil {
			return fmt.Errorf("workspace: %w", err)
		}

		// Load postpone config.
		postponed := loadPostponeConfig(filepath.Join(workspace, "system", "heartbeat-postpone.json"))

		// Collect sessions (same logic as scheduler).
		opts := listSessionsOpts{Days: 2, UserOnly: true}
		sessions, err := collectSessions(cfg, opts)
		if err != nil {
			return fmt.Errorf("collect sessions: %w", err)
		}

		now := time.Now()

		type entry struct {
			Key          string `json:"key"`
			LastActive   string `json:"last_active"`
			NextPulse    string `json:"next_pulse"`
			Status       string `json:"status"`
			HasHeartbeat bool   `json:"has_heartbeat"`
		}
		var entries []entry

		for _, se := range sessions.Sessions {
			if se.LastUserActiveAt == nil {
				continue
			}
			lastActive, parseErr := time.Parse(time.RFC3339, *se.LastUserActiveAt)
			if parseErr != nil {
				continue
			}

			e := entry{
				Key:          se.Key,
				LastActive:   lastActive.Local().Format("15:04"),
				HasHeartbeat: se.HasHeartbeat,
			}

			// Check inactivity window.
			if now.Sub(lastActive) > hbActivityWindow {
				e.Status = "inactive (>48h)"
				e.NextPulse = "-"
				entries = append(entries, e)
				continue
			}

			// Check postpone.
			if until, ok := postponed[se.Key]; ok {
				if t, parseErr := time.Parse(time.RFC3339, until); parseErr == nil && now.Before(t) {
					e.Status = fmt.Sprintf("postponed until %s", t.Local().Format("15:04"))
					e.NextPulse = t.Local().Format("15:04")
					entries = append(entries, e)
					continue
				}
			}

			// Compute next pulse using 10+30+30... schedule.
			firstPulse := lastActive.Add(hbQuietMin)
			var nextPulse time.Time
			if !firstPulse.Before(now) {
				nextPulse = firstPulse
			} else {
				next := firstPulse
				for next.Before(now) {
					next = next.Add(hbPulseInterval)
				}
				nextPulse = next
			}

			// Check quiet threshold.
			if now.Sub(lastActive) < hbQuietMin {
				e.Status = "user active"
				e.NextPulse = firstPulse.Local().Format("15:04")
			} else {
				e.Status = "scheduled"
				e.NextPulse = nextPulse.Local().Format("15:04")
			}

			entries = append(entries, e)
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	},
}

func init() {
	heartbeatCmd.AddCommand(heartbeatPostponeCmd)
	heartbeatCmd.AddCommand(heartbeatStatusCmd)
	rootCmd.AddCommand(heartbeatCmd)
}
