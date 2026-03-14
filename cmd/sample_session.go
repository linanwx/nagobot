package cmd

import (
	"fmt"

	"github.com/linanwx/nagobot/config"
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
		fmt.Printf("---\ncommand: sample-session\nstatus: empty\nsession: %s\ntotal: %d\n---\n\nNo messages (all filtered).\n",
			key, totalCount)
		return nil
	}

	count := min(sampleSessionCount, filteredCount)

	// Evenly spaced indices.
	indices := evenlySpacedIndices(filteredCount, count)
	step := "all"
	if filteredCount > count {
		step = fmt.Sprintf("every %d", filteredCount/count)
	}

	fmt.Printf("---\ncommand: sample-session\nstatus: ok\nsession: %s\nsampled: %d\nfiltered: %d\ntotal: %d\nstep: %s\n---\n\n",
		key, count, filteredCount, totalCount, step)

	for _, idx := range indices {
		m := filtered[idx]
		content, _ := truncateContent(m.Content, defaultTruncateLen)
		msgID := m.ID
		if msgID == "" {
			msgID = "-"
		}
		fmt.Printf("[%d] (%s) %s: %s\n", idx+1, msgID, m.Role, content)
	}

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
