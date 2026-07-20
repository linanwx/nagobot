package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// loginLinkResponse is the RPC payload for auth.loginlink.
type loginLinkResponse struct {
	Link    string    `json:"link"`
	Expires time.Time `json:"expires"`
}

// loginLinkCmd mints a one-time web login link via the running daemon.
// Deliberately a hard CLI command, not an LLM-callable tool: credential
// issuance stays out of the model's hands.
var loginLinkCmd = &cobra.Command{
	Use:   "login-link",
	Short: "Mint a one-time web login link (30 min, single use)",
	Long: `Mint a one-time login link for the web UI.

Open the link in a browser to create a new user or claim an existing one,
then register a passkey — the passkey is the durable login for that browser
profile. The link is single-use and expires after 30 minutes; mint as many
as you need.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := rpcCall("auth.loginlink", nil)
		if err != nil {
			return fmt.Errorf("is the daemon running? %w", err)
		}
		var resp loginLinkResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), resp.Link)
		fmt.Fprintf(cmd.OutOrStdout(), "expires %s (single use)\n", resp.Expires.Local().Format("15:04"))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(loginLinkCmd)
}
