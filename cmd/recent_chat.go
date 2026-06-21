package cmd

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/tools"
	"github.com/spf13/cobra"
)

var (
	recentChatSince    time.Duration
	recentChatTail     int
	recentChatMaxChars int
)

var recentChatCmd = &cobra.Command{
	Use:     "recent-chat",
	Short:   "Dump recent chat history across all sessions, time-filtered and readable",
	GroupID: "internal",
	Args:    cobra.NoArgs,
	RunE:    runRecentChat,
}

func init() {
	recentChatCmd.Flags().DurationVar(&recentChatSince, "since", 24*time.Hour, "Only include entries newer than this (e.g. 24h, 48h)")
	recentChatCmd.Flags().IntVar(&recentChatTail, "tail", 200, "Cap to the last N recent entries per session")
	recentChatCmd.Flags().IntVar(&recentChatMaxChars, "max-chars", 1000, "Truncate each entry to this many characters (0 = no cap)")
	rootCmd.AddCommand(recentChatCmd)
}

func runRecentChat(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}
	sessionsDir := filepath.Join(workspace, "sessions")

	// Find every chat.jsonl under the sessions directory.
	var dirs []string
	_ = filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == "chat.jsonl" {
			dirs = append(dirs, filepath.Dir(path))
		}
		return nil
	})
	sort.Strings(dirs)

	var sb strings.Builder
	sessionsWithActivity := 0
	for _, dir := range dirs {
		block := session.ReadRecentChatSince(dir, recentChatTail, recentChatSince, recentChatMaxChars, time.Local)
		if block == "" {
			continue
		}
		sessionsWithActivity++
		label := strings.TrimPrefix(dir, sessionsDir+string(filepath.Separator))
		fmt.Fprintf(&sb, "\n===== SESSION: %s =====\n%s\n", label, block)
	}

	body := strings.TrimSpace(sb.String())
	if body == "" {
		body = "No chat activity in the given window."
	}
	fields := map[string]any{
		"since":                  recentChatSince.String(),
		"sessions_with_activity": sessionsWithActivity,
	}
	fmt.Print(tools.CmdResult("recent-chat", fields, body))
	return nil
}
