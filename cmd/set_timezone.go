package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

var setTimezoneCmd = &cobra.Command{
	Use:     "set-timezone",
	Short:   "Set or clear the timezone for a session",
	GroupID: "internal",
	Long: `Set the IANA timezone for a session key in config.yaml.

The running server detects config changes automatically, so the new timezone
takes effect on the next message in that session.

Examples:
  nagobot set-timezone --session "discord:123456" --timezone "Asia/Shanghai"
  nagobot set-timezone --session "telegram:78910" --timezone "America/New_York"
  nagobot set-timezone --session "discord:123456"                              # clear (use system default)`,
	RunE: runSetTimezone,
}

var (
	setTimezoneSession string
	setTimezoneName    string
)

func init() {
	setTimezoneCmd.Flags().StringVar(&setTimezoneSession, "session", "", "Session key (required)")
	setTimezoneCmd.Flags().StringVar(&setTimezoneName, "timezone", "", "IANA timezone (e.g. Asia/Shanghai, empty to clear)")
	_ = setTimezoneCmd.MarkFlagRequired("session")
	rootCmd.AddCommand(setTimezoneCmd)
}

func runSetTimezone(_ *cobra.Command, _ []string) error {
	session := strings.TrimSpace(setTimezoneSession)
	if session == "" {
		return fmt.Errorf("--session is required")
	}

	tz := strings.TrimSpace(setTimezoneName)
	if tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return fmt.Errorf("invalid timezone %q: %w", tz, err)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if cfg.Channels == nil {
		cfg.Channels = &config.ChannelsConfig{}
	}
	if cfg.Channels.SessionTimezones == nil {
		cfg.Channels.SessionTimezones = make(map[string]string)
	}

	if tz == "" {
		delete(cfg.Channels.SessionTimezones, session)
	} else {
		cfg.Channels.SessionTimezones[session] = tz
	}

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	if tz == "" {
		fmt.Printf("---\ncommand: set-timezone\nstatus: ok\nsession: %s\ntimezone: cleared\n---\n\nCleared timezone for session %q.\n", session, session)
	} else {
		fmt.Printf("---\ncommand: set-timezone\nstatus: ok\nsession: %s\ntimezone: %s\n---\n\nSet timezone %q for session %q.\n", session, tz, tz, session)
	}
	return nil
}
