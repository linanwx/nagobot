package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/spf13/cobra"
)

var compressSessionCmd = &cobra.Command{
	Use:   "compress-session <session-file> <input-file>",
	Short: "Replace session messages with a compressed summary",
	Long: `Replace session messages with compressed context from an input text file.
The original is backed up to <session_dir>/history/.

The input file should contain plain text (not JSON). It will be stored as a
single assistant message in the session. The input file is deleted after use.

Example:
  nagobot compress-session /path/to/session.json /path/to/compressed.txt`,
	Args:    cobra.ExactArgs(2),
	GroupID: "internal",
	RunE:    runCompressSession,
}

func init() {
	rootCmd.AddCommand(compressSessionCmd)
}

func runCompressSession(_ *cobra.Command, args []string) error {
	sessionFile := args[0]
	inputFile := args[1]

	// 1. Read compressed context.
	inputData, err := os.ReadFile(inputFile)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}
	content := strings.TrimSpace(string(inputData))
	if content == "" {
		return fmt.Errorf("input file is empty")
	}

	// 2. Read original session.
	origData, err := os.ReadFile(sessionFile)
	if err != nil {
		return fmt.Errorf("failed to read session file: %w", err)
	}
	var orig session.Session
	if err := json.Unmarshal(origData, &orig); err != nil {
		return fmt.Errorf("failed to parse session file: %w", err)
	}
	origCount := len(orig.Messages)

	// 3. Backup original.
	sessionDir := filepath.Dir(sessionFile)
	historyDir := filepath.Join(sessionDir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return fmt.Errorf("failed to create history directory: %w", err)
	}
	now := time.Now()
	timestamp := fmt.Sprintf("%d_%s", now.Unix(), now.Format("20060102T150405-0700"))
	backupPath := filepath.Join(historyDir, timestamp+".json")
	if err := os.WriteFile(backupPath, origData, 0644); err != nil {
		return fmt.Errorf("failed to write backup: %w", err)
	}

	// 4. Keep tail 25% of original messages, prepend compressed summary.
	tailCount := origCount / 4
	cutoff := origCount - tailCount
	tail := orig.Messages[cutoff:]
	orig.Messages = append([]provider.Message{{Role: "assistant", Content: content}}, tail...)
	orig.UpdatedAt = now
	newData, err := json.MarshalIndent(&orig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal new session: %w", err)
	}
	if err := os.WriteFile(sessionFile, newData, 0644); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}

	// 5. Cleanup input file.
	_ = os.Remove(inputFile)

	// 6. Summary.
	fmt.Printf("Session compressed: %d → %d messages\n", origCount, len(orig.Messages))
	fmt.Printf("Backup: %s\n", backupPath)
	fmt.Printf("Session: %s\n", sessionFile)
	return nil
}
