package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/spf13/cobra"
)

const defaultTruncateLen = 500

var (
	readSessionOffset int
	readSessionLimit  int
	readSessionTail   int
	readSessionFull   bool
)

var readSessionCmd = &cobra.Command{
	Use:     "read-session <key>",
	Short:   "Read filtered chat history of a session",
	GroupID: "internal",
	Args:    cobra.ExactArgs(1),
	RunE:    runReadSession,
}

func init() {
	readSessionCmd.Flags().IntVar(&readSessionOffset, "offset", 0, "Start from the Nth filtered message")
	readSessionCmd.Flags().IntVar(&readSessionLimit, "limit", 20, "Number of messages to return")
	readSessionCmd.Flags().IntVar(&readSessionTail, "tail", 0, "Show last N messages (overrides offset)")
	readSessionCmd.Flags().BoolVar(&readSessionFull, "full", false, "Show full message content without truncation")
	rootCmd.AddCommand(readSessionCmd)
}

func runReadSession(_ *cobra.Command, args []string) error {
	key := args[0]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	messages, totalCount, err := loadSessionMessages(workspace, key)
	if err != nil {
		return err
	}

	filtered := filterToolMessages(messages)
	filteredCount := len(filtered)

	if readSessionTail > 0 {
		readSessionOffset = filteredCount - readSessionTail
		if readSessionOffset < 0 {
			readSessionOffset = 0
		}
		readSessionLimit = filteredCount - readSessionOffset
	}

	if readSessionOffset >= filteredCount {
		fmt.Printf("No messages at offset %d. Total filtered messages: %d (from %d total).\n",
			readSessionOffset, filteredCount, totalCount)
		return nil
	}

	end := min(readSessionOffset+readSessionLimit, filteredCount)
	page := filtered[readSessionOffset:end]

	truncatedCount := 0
	for i, m := range page {
		idx := readSessionOffset + i + 1
		var content string
		if readSessionFull {
			content = strings.TrimSpace(m.Content)
		} else {
			var truncated int
			content, truncated = truncateContent(m.Content, defaultTruncateLen)
			if truncated > 0 {
				truncatedCount++
			}
		}
		fmt.Printf("[%d] %s: %s\n", idx, m.Role, content)
	}

	remaining := filteredCount - end
	fmt.Printf("---\nShowing messages %d-%d of %d (filtered from %d total).",
		readSessionOffset+1, end, filteredCount, totalCount)
	if remaining > 0 {
		fmt.Printf(" %d remaining.\nNext: nagobot read-session %q --offset %d --limit %d",
			remaining, key, end, readSessionLimit)
	} else {
		fmt.Print(" End of session.")
	}
	if truncatedCount > 0 {
		fmt.Printf("\n%d message(s) truncated to %d chars. Use --full to show complete content.", truncatedCount, defaultTruncateLen)
	}
	fmt.Println()

	return nil
}

// loadSessionMessages reads a session JSONL file and returns raw messages + total count.
func loadSessionMessages(workspace, key string) ([]provider.Message, int, error) {
	sessionsDir := filepath.Join(workspace, "sessions")
	parts := strings.Split(key, ":")
	pathParts := append([]string{sessionsDir}, parts...)
	pathParts = append(pathParts, session.SessionFileName)
	sessionPath := filepath.Join(pathParts...)

	s, err := session.ReadFile(sessionPath)
	if err != nil {
		return nil, 0, fmt.Errorf("session %q not found: %w", key, err)
	}
	return s.Messages, len(s.Messages), nil
}

// filterToolMessages removes tool-role messages and assistant messages that only contain tool calls.
func filterToolMessages(messages []provider.Message) []provider.Message {
	var result []provider.Message
	for _, m := range messages {
		if m.Role == "tool" {
			continue
		}
		if m.Role == "system" {
			continue
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && strings.TrimSpace(m.Content) == "" {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		result = append(result, m)
	}
	return result
}

// truncateContent returns the (possibly truncated) string and the number of characters truncated.
func truncateContent(s string, maxLen int) (string, int) {
	s = strings.TrimSpace(s)
	// Collapse newlines for compact display.
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "...", len(s) - maxLen
	}
	return s, 0
}
