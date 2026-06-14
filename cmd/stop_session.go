package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// sessionStopParams is the RPC request for the session.stop method.
type sessionStopParams struct {
	SessionKey string `json:"session_key"`
}

// sessionStopResponse is the RPC reply for the session.stop method.
type sessionStopResponse struct {
	SessionKey string `json:"session_key"`
	Note       string `json:"note"`
}

var stopSessionCmd = &cobra.Command{
	Use:   "stop-session <session-key>",
	Short: "Soft-stop a running session by asking its turn to terminate",
	Long: `Soft-stop a running session (e.g. a subagent at <parent>:threads:<task_id>).

This injects a control message into the session's dedicated inject lane. At the
running turn's next iteration boundary, the session's LLM is asked to end the
turn immediately via dispatch({}). It is a SOFT stop: any in-flight tool or LLM
call runs to completion before the boundary is reached, so the stop is not
instantaneous. The session is not hard-cancelled and its history stays valid.`,
	Args:    cobra.ExactArgs(1),
	GroupID: "internal",
	RunE: func(_ *cobra.Command, args []string) error {
		key := args[0]
		result, err := rpcCall("session.stop", sessionStopParams{SessionKey: key})
		if err != nil {
			return fmt.Errorf("stop-session: %w", err)
		}
		var resp sessionStopResponse
		if err := json.Unmarshal(result, &resp); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		fmt.Printf("Stop requested for session %q: %s\n", resp.SessionKey, resp.Note)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopSessionCmd)
}
