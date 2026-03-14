package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/monitor"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread/msg"
	"github.com/spf13/cobra"
)

var clearFlag bool

var compressSessionCmd = &cobra.Command{
	Use:   "compress-session <session-file> [input-file]",
	Short: "Replace session messages with a compressed summary",
	Long: `Replace session messages with compressed context from an input text file.
The original is backed up to <session_dir>/history/.

Use --clear to discard all messages without an input file.

Example:
  nagobot compress-session /path/to/session.jsonl /path/to/compressed.txt
  nagobot compress-session --clear /path/to/session.jsonl`,
	Args:    cobra.RangeArgs(1, 2),
	GroupID: "internal",
	RunE:    runCompressSession,
}

func init() {
	compressSessionCmd.Flags().BoolVar(&clearFlag, "clear", false, "Clear all messages without an input file")
	rootCmd.AddCommand(compressSessionCmd)
}

func runCompressSession(_ *cobra.Command, args []string) error {
	sessionFile := args[0]

	if !clearFlag && len(args) < 2 {
		return fmt.Errorf("input file required (or use --clear)")
	}

	// 1. Read original session.
	orig, err := session.ReadFile(sessionFile)
	if err != nil {
		return fmt.Errorf("failed to read session file: %w", err)
	}
	orig.Key = session.DeriveKeyFromPath(sessionFile)
	origCount := len(orig.Messages)
	origMessages := make([]provider.Message, origCount)
	copy(origMessages, orig.Messages)

	// 2. Backup original.
	origData, err := os.ReadFile(sessionFile)
	if err != nil {
		return fmt.Errorf("failed to read session file for backup: %w", err)
	}
	sessionDir := filepath.Dir(sessionFile)
	historyDir := filepath.Join(sessionDir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return fmt.Errorf("failed to create history directory: %w", err)
	}
	now := time.Now()
	timestamp := fmt.Sprintf("%d_%s", now.Unix(), now.Format("20060102T150405-0700"))
	backupPath := filepath.Join(historyDir, timestamp+".jsonl")
	if err := os.WriteFile(backupPath, origData, 0644); err != nil {
		return fmt.Errorf("failed to write backup: %w", err)
	}

	// 3. Build new messages.
	if clearFlag {
		orig.Messages = []provider.Message{}
	} else {
		inputFile := args[1]
		inputData, err := os.ReadFile(inputFile)
		if err != nil {
			return fmt.Errorf("failed to read input file: %w", err)
		}
		content := strings.TrimSpace(string(inputData))
		if content == "" {
			return fmt.Errorf("input file is empty")
		}

		tailCount := origCount / 4
		cutoff := origCount - tailCount
		// Adjust cutoff to avoid splitting a tool_calls→tool sequence.
		for cutoff > 0 && cutoff < origCount && orig.Messages[cutoff].Role == "tool" {
			cutoff--
		}
		if cutoff > 0 && cutoff < origCount && orig.Messages[cutoff-1].Role == "assistant" && len(orig.Messages[cutoff-1].ToolCalls) > 0 {
			cutoff--
		}
		// Ensure tail starts with a user message (required by some providers).
		for cutoff < origCount && orig.Messages[cutoff].Role != "user" {
			cutoff++
		}
		tail := orig.Messages[cutoff:]
		newMessages := make([]provider.Message, 0, len(tail)+1)
		newMessages = append(newMessages, tail...)
		wrappedContent := msg.BuildSystemMessage("compression_summary", nil, content)
		newMessages = append(newMessages, provider.Message{Role: "assistant", Content: wrappedContent, Timestamp: now})
		orig.Messages = newMessages

		// 4. Append summary to daily memory file.
		memoryDir := filepath.Join(sessionDir, "memory")
		if err := os.MkdirAll(memoryDir, 0755); err != nil {
			logger.Warn("compress-session: failed to create memory directory", "err", err)
		} else {
			memoryFile := filepath.Join(memoryDir, now.Format("2006-01-02")+".md")
			f, err := os.OpenFile(memoryFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				logger.Warn("compress-session: failed to open memory file", "err", err)
			} else {
				defer f.Close()
				// Use separator newline only when appending to existing content.
				info, _ := f.Stat()
				sep := ""
				if info != nil && info.Size() > 0 {
					sep = "\n"
				}
				header := fmt.Sprintf("%s## Compression %s\n\n", sep, now.Format("15:04"))
				_, _ = f.WriteString(header + content + "\n")
			}
		}

		_ = os.Remove(inputFile)
	}

	if err := session.WriteFile(sessionFile, orig); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}

	// 5. Record compression metrics.
	record := buildCompressionRecord(now, orig.Key, origMessages, len(orig.Messages))
	cfg, cfgErr := config.Load()
	if cfgErr == nil {
		if ws, wsErr := cfg.WorkspacePath(); wsErr == nil {
			store := monitor.NewStore(filepath.Join(ws, "metrics"))
			store.RecordCompression(record)
		}
	}

	logger.Info("compress-session completed",
		"sessionKey", orig.Key,
		"messagesBefore", origCount,
		"messagesAfter", len(orig.Messages),
		"estimatedTokens", record.EstimatedTokens,
		"totalChars", record.TotalChars,
		"maxMsgChars", record.MaxMsgChars,
		"backup", backupPath,
		"session", sessionFile,
	)
	fmt.Printf("---\ncommand: compress-session\nstatus: ok\nmessages_before: %d\nmessages_after: %d\nestimated_tokens: %d\nbackup: %s\nsession: %s\n---\n",
		origCount, len(orig.Messages), record.EstimatedTokens, backupPath, sessionFile)
	return nil
}

func buildCompressionRecord(ts time.Time, sessionKey string, messages []provider.Message, messagesAfter int) monitor.CompressionRecord {
	roleCounts := map[string]int{}
	var totalChars, maxChars int
	var maxRole, maxPreview string

	for _, m := range messages {
		roleCounts[m.Role]++
		// Use Compressed content if available (reflects Tier 1 compression).
		content := m.Content
		if m.Compressed != "" {
			content = m.Compressed
		}
		msgLen := len(content)
		for _, tc := range m.ToolCalls {
			msgLen += len(tc.Function.Arguments)
		}
		totalChars += msgLen
		if msgLen > maxChars {
			maxChars = msgLen
			maxRole = m.Role
			preview := content
			if m.Role == "tool" && len(preview) > 200 {
				// For tool results, try to show the tool call ID for identification.
				preview = m.ToolCallID + ": " + preview[:150]
			}
			runes := []rune(preview)
			if len(runes) > 120 {
				preview = string(runes[:120]) + "..."
			}
			maxPreview = preview
		}
	}

	avgChars := 0
	if len(messages) > 0 {
		avgChars = totalChars / len(messages)
	}

	// Estimate tokens: ~4 chars per token for mixed content, plus structural overhead.
	estimatedTokens := totalChars/4 + len(messages)*6 + 3

	return monitor.CompressionRecord{
		Timestamp:       ts,
		SessionKey:      sessionKey,
		MessagesBefore:  len(messages),
		MessagesAfter:   messagesAfter,
		EstimatedTokens: estimatedTokens,
		TotalChars:      totalChars,
		RoleCounts:      roleCounts,
		MaxMsgChars:     maxChars,
		MaxMsgRole:      maxRole,
		MaxMsgPreview:   maxPreview,
		AvgMsgChars:     avgChars,
	}
}
