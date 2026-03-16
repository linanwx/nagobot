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

func reflectInstruction() string {
	return `You must call use_skill("heartbeat-reflect") and follow its instructions.`
}

func wakeInstruction() string {
	return `You must call use_skill("heartbeat-wake") and follow its instructions.`
}

var heartbeatCmd = &cobra.Command{
	Use:     "heartbeat",
	Short:   "Heartbeat operations for user sessions",
	GroupID: "internal",
}

// updateHeartbeatState atomically reads, merges, and writes system/heartbeat-state.json.
// It sets state[field][key] = now and returns (filePath, timestamp, error).
func updateHeartbeatState(field, key string) (string, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", "", fmt.Errorf("load config: %w", err)
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return "", "", fmt.Errorf("workspace path: %w", err)
	}

	statePath := filepath.Join(workspace, "system", "heartbeat-state.json")

	// Read existing state or start empty.
	state := make(map[string]map[string]string)
	if data, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(data, &state)
	}

	if state[field] == nil {
		state[field] = make(map[string]string)
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	state[field][key] = ts

	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		return "", "", fmt.Errorf("mkdir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal: %w", err)
	}
	tmp := statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return "", "", fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, statePath); err != nil {
		return "", "", fmt.Errorf("rename: %w", err)
	}

	return statePath, ts, nil
}

var heartbeatReflectCmd = &cobra.Command{
	Use:   "reflect <session-key>",
	Short: "Trigger heartbeat reflection for a session",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		key := args[0]
		_, err := rpcCall("heartbeat.reflect", map[string]string{"key": key})
		if err != nil {
			return fmt.Errorf("heartbeat reflect: %w", err)
		}

		filePath, ts, err := updateHeartbeatState("last_reflection", key)
		if err != nil {
			return fmt.Errorf("update heartbeat state: %w", err)
		}

		fmt.Printf("---\ntool: heartbeat_reflect\nstatus: ok\nsession: %s\nfile: %s\nupdated_field: last_reflection\ntimestamp: %s\n---\n\n", key, filePath, ts)
		fmt.Printf("Reflection triggered for session %q. Timestamp updated automatically — do not write heartbeat-state.json manually.\n", key)
		return nil
	},
}

var heartbeatWakeCmd = &cobra.Command{
	Use:   "wake <session-key>",
	Short: "Trigger heartbeat wake for a session",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		key := args[0]
		_, err := rpcCall("heartbeat.wake", map[string]string{"key": key})
		if err != nil {
			return fmt.Errorf("heartbeat wake: %w", err)
		}

		fmt.Printf("---\ntool: heartbeat_wake\nstatus: ok\nsession: %s\n---\n\n", key)
		fmt.Printf("Wake triggered for session %q.\n", key)
		return nil
	},
}

func init() {
	heartbeatCmd.AddCommand(heartbeatReflectCmd)
	heartbeatCmd.AddCommand(heartbeatWakeCmd)
	rootCmd.AddCommand(heartbeatCmd)
}
