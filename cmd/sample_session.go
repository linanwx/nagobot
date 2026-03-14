package cmd

import (
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/config"
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

	var sb strings.Builder
	for _, idx := range indices {
		m := filtered[idx]
		content, _ := truncateContent(m.Content, defaultTruncateLen)
		msgID := messageIDOrDash(m.ID)
		fmt.Fprintf(&sb, "[%d] (%s) %s: %s\n", idx+1, msgID, m.Role, content)
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
