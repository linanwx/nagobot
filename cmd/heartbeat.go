package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func reflectInstruction(sessionDir string) string {
	return fmt.Sprintf(`Load skill "heartbeat-reflect" and follow its instructions. Session directory: %s`, sessionDir)
}

func wakeInstruction(sessionDir string) string {
	return fmt.Sprintf(`Load skill "heartbeat-wake" and follow its instructions. Session directory: %s`, sessionDir)
}

var heartbeatCmd = &cobra.Command{
	Use:     "heartbeat",
	Short:   "Heartbeat operations for user sessions",
	GroupID: "internal",
}

func newHeartbeatSubcommand(use, short, rpcMethod string) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <session-key>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			_, err := rpcCall(rpcMethod, map[string]string{"key": args[0]})
			if err != nil {
				return fmt.Errorf("heartbeat %s: %w", use, err)
			}
			fmt.Println("OK")
			return nil
		},
	}
}

func init() {
	heartbeatCmd.AddCommand(newHeartbeatSubcommand("reflect", "Trigger heartbeat reflection for a session", "heartbeat.reflect"))
	heartbeatCmd.AddCommand(newHeartbeatSubcommand("wake", "Trigger heartbeat wake for a session", "heartbeat.wake"))
	rootCmd.AddCommand(heartbeatCmd)
}
