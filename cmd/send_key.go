package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// sendKeyParams is the RPC payload for auth.sendkey.create / auth.sendkey.revoke.
type sendKeyParams struct {
	User    string `json:"user"`
	Session string `json:"session,omitempty"`
}

type sendKeyCreateResponse struct {
	Key     string `json:"key"`
	User    string `json:"user"`
	Session string `json:"session"`
}

type sendKeyRevokeResponse struct {
	Removed int `json:"removed"`
}

var (
	sendKeyUser    string
	sendKeySession string
)

// sendKeyCmd manages bearer keys for POST /api/send. Like login-link, a hard
// CLI command and never an LLM tool: credential issuance stays out of the
// model's hands.
var sendKeyCmd = &cobra.Command{
	Use:   "send-key",
	Short: "Manage keys for posting messages via POST /api/send",
	Long: `Manage send keys: bearer keys that let a device with no browser login
(e.g. an Apple Watch shortcut) post a message into one session as one person.

A key is bound to a person and a session. It can post into that session and
nothing else; it cannot read history. Replies go wherever that session's
replies always go (a discord: session answers on Discord).

  POST /api/send
  Authorization: Bearer <key>
  Content-Type: application/json      {"text": "..."}
  Content-Type: audio/*               raw recording as the body`,
}

var sendKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Mint a send key (shown once)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(sendKeyUser) == "" || strings.TrimSpace(sendKeySession) == "" {
			return fmt.Errorf("--user and --session are required")
		}
		raw, err := rpcCall("auth.sendkey.create", sendKeyParams{User: sendKeyUser, Session: sendKeySession})
		if err != nil {
			return err
		}
		var resp sendKeyCreateResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), resp.Key)
		fmt.Fprintf(cmd.OutOrStdout(), "user %s → session %s (shown once; only its hash is stored)\n", resp.User, resp.Session)
		return nil
	},
}

var sendKeyRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke a user's send keys (all, or only those bound to --session)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(sendKeyUser) == "" {
			return fmt.Errorf("--user is required")
		}
		raw, err := rpcCall("auth.sendkey.revoke", sendKeyParams{User: sendKeyUser, Session: sendKeySession})
		if err != nil {
			return err
		}
		var resp sendKeyRevokeResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "revoked %d key(s)\n", resp.Removed)
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{sendKeyCreateCmd, sendKeyRevokeCmd} {
		c.Flags().StringVar(&sendKeyUser, "user", "", "web username the key acts as")
		c.Flags().StringVar(&sendKeySession, "session", "", "session key the key posts into")
		sendKeyCmd.AddCommand(c)
	}
	rootCmd.AddCommand(sendKeyCmd)
}
