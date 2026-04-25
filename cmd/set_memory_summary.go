package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/linanwx/nagobot/thread/msg"
	"github.com/linanwx/nagobot/tools"
	"github.com/spf13/cobra"
)

var setMemorySummaryCmd = &cobra.Command{
	Use:     "set-memory-summary <file-path> <summary>",
	Short:   "Add a summary to a memory file's YAML frontmatter",
	GroupID: "internal",
	Args:    cobra.ExactArgs(2),
	RunE:    runSetMemorySummary,
}

func init() {
	rootCmd.AddCommand(setMemorySummaryCmd)
}

func runSetMemorySummary(_ *cobra.Command, args []string) error {
	filePath := args[0]
	summary := strings.TrimSpace(args[1])
	if summary == "" {
		return fmt.Errorf("summary is required")
	}
	// Strip newlines — frontmatter values must be single-line.
	summary = strings.ReplaceAll(summary, "\n", " ")

	if hasMemorySummary(filePath) {
		return fmt.Errorf("file already has a summary")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	content := addSummaryToFrontmatter(string(data), summary)

	tmp := filePath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := os.Rename(tmp, filePath); err != nil {
		return fmt.Errorf("failed to rename: %w", err)
	}

	fmt.Print(tools.CmdResult("set-memory-summary", map[string]any{"file": filePath}, "Summary saved.") + "\n")
	return nil
}

// addSummaryToFrontmatter inserts a `summary:` field into existing frontmatter
// or wraps the file in fresh frontmatter. All YAML construction goes through
// msg.* helpers so quoting is handled correctly.
func addSummaryToFrontmatter(data, summary string) string {
	if mapping, body, ok := msg.ParseFrontmatter(data); ok {
		msg.AppendScalarPair(mapping, "summary", summary)
		return msg.BuildFrontmatter(mapping, body)
	}
	mapping := msg.NewMapping()
	msg.AppendScalarPair(mapping, "summary", summary)
	body := strings.TrimLeft(data, "\n")
	return msg.BuildFrontmatter(mapping, "\n"+body)
}
