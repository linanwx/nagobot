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

func init() {
	heartbeatCmd.AddCommand(heartbeatPostponeCmd)
	rootCmd.AddCommand(heartbeatCmd)
}
