package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/thread"
	"github.com/linanwx/nagobot/tools"
	"github.com/spf13/cobra"
)

var sampleSessionCount int

var sampleSessionCmd = &cobra.Command{
	Use:     "sample-session <key>",
	Short:   "Evenly sample filtered chat history of a session",
	GroupID: "internal",
	Args:    cobra.ExactArgs(1),
	RunE:    runSampleSession,
}

func init() {
	sampleSessionCmd.Flags().IntVar(&sampleSessionCount, "count", 20, "Number of messages to sample")
	rootCmd.AddCommand(sampleSessionCmd)
}

func runSampleSession(_ *cobra.Command, args []string) error {
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

	if filteredCount == 0 {
		fmt.Print(tools.CmdResult("sample-session", map[string]any{
			"session": key,
			"total":   totalCount,
		}, "No messages (all filtered)."))
		return nil
	}

	count := min(sampleSessionCount, filteredCount)
	indices := evenlySpacedIndices(filteredCount, count)
	step := "all"
	if filteredCount > count {
		step = fmt.Sprintf("every %d", filteredCount/count)
	}

	// Track sampled indices to avoid duplicates in tail section.
	sampledSet := make(map[int]bool, len(indices))
	for _, idx := range indices {
		sampledSet[idx] = true
	}

	var sb strings.Builder
	for _, idx := range indices {
		m := filtered[idx]
		content, _ := truncateContent(bodyFromFrontmatter(m.Content), defaultTruncateLen)
		fmt.Fprintf(&sb, "[%d] %s: %s\n", idx+1, m.Role, content)
	}

	// Append last 5 messages not already sampled.
	const tailCount = 5
	var tailMessages []int
	for i := filteredCount - 1; i >= 0 && len(tailMessages) < tailCount; i-- {
		if !sampledSet[i] {
			tailMessages = append(tailMessages, i)
		}
	}
	if len(tailMessages) > 0 {
		slices.Reverse(tailMessages)
		sb.WriteString("\n--- recent (last 5 not in sample) ---\n")
		for _, idx := range tailMessages {
			m := filtered[idx]
			content, _ := truncateContent(bodyFromFrontmatter(m.Content), defaultTruncateLen)
			fmt.Fprintf(&sb, "[%d] %s: %s\n", idx+1, m.Role, content)
		}
	}

	fmt.Print(tools.CmdResult("sample-session", map[string]any{
		"session":  key,
		"sampled":  count,
		"filtered": filteredCount,
		"total":    totalCount,
		"step":     step,
	}, sb.String()))
	return nil
}

// bodyFromFrontmatter recursively strips YAML frontmatter layers, returning only the final body.
// Trims leading newlines between layers (wake payloads insert a blank line after closing ---).
func bodyFromFrontmatter(content string) string {
	for {
		_, body, ok := thread.SplitFrontmatter(content)
		if !ok {
			return content
		}
		content = strings.TrimLeft(body, "\n")
	}
}

// evenlySpacedIndices returns count evenly-spaced indices from [0, total).
func evenlySpacedIndices(total, count int) []int {
	if count >= total {
		indices := make([]int, total)
		for i := range total {
			indices[i] = i
		}
		return indices
	}
	indices := make([]int, count)
	for i := range count {
		indices[i] = i * (total - 1) / (count - 1)
	}
	return indices
}
