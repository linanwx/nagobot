package cmd

import (
	"fmt"
	"os"
	"strings"

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

	// Quote summary value to safely handle colons and special YAML characters.
	quotedSummary := "\"" + strings.ReplaceAll(summary, "\"", "\\\"") + "\""

	var content string
	if strings.HasPrefix(string(data), "---\n") {
		// Has existing frontmatter — insert summary before closing ---.
		idx := strings.Index(string(data)[4:], "\n---\n")
		if idx >= 0 {
			yamlEnd := 4 + idx
			content = string(data[:yamlEnd]) + "\nsummary: " + quotedSummary + string(data[yamlEnd:])
		} else {
			content = "---\nsummary: " + quotedSummary + "\n---\n\n" + string(data)
		}
	} else {
		content = "---\nsummary: " + quotedSummary + "\n---\n\n" + string(data)
	}

	tmp := filePath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := os.Rename(tmp, filePath); err != nil {
		return fmt.Errorf("failed to rename: %w", err)
	}

	fmt.Printf("---\ncommand: set-memory-summary\nstatus: ok\nfile: %s\n---\n\nSummary saved.\n", filePath)
	return nil
}
