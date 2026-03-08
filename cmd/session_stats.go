package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread"
	"github.com/spf13/cobra"
)

var sessionStatsCmd = &cobra.Command{
	Use:     "session-stats <key>",
	Short:   "Show context usage stats for a session",
	GroupID: "internal",
	Args:    cobra.ExactArgs(1),
	RunE:    runSessionStats,
}

func init() {
	rootCmd.AddCommand(sessionStatsCmd)
}

type sessionStatsOutput struct {
	SessionKey          string         `json:"session_key"`
	MessageCount        int            `json:"message_count"`
	RoleCounts          map[string]int `json:"role_counts"`
	CompressedMessages  int            `json:"compressed_messages"`
	RawTokens           int            `json:"raw_tokens"`
	CompressedTokens    int            `json:"compressed_tokens"`
	TokensSaved         int            `json:"tokens_saved"`
	ContextWindowTokens int            `json:"context_window_tokens"`
	UsageRatio          float64        `json:"usage_ratio"`
	WarnRatio           float64        `json:"warn_ratio"`
	PressureStatus      string         `json:"pressure_status"`
}

func runSessionStats(_ *cobra.Command, args []string) error {
	key := args[0]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	messages, _, err := loadSessionMessages(workspace, key)
	if err != nil {
		return err
	}

	roleCounts := map[string]int{}
	compressedCount := 0
	for _, m := range messages {
		roleCounts[m.Role]++
		if m.Compressed != "" {
			compressedCount++
		}
	}

	rawTokens := thread.EstimateMessagesTokens(messages)
	compressed := thread.ApplyCompressed(provider.SanitizeMessages(messages))
	compressedTokens := thread.EstimateMessagesTokens(compressed)

	contextWindow := cfg.GetContextWindowTokens()
	warnRatio := cfg.GetContextWarnRatio()
	usageRatio := float64(compressedTokens) / float64(contextWindow)

	status := "ok"
	if usageRatio >= warnRatio {
		status = "pressure"
	} else if usageRatio >= warnRatio*0.8 {
		status = "warning"
	}

	output := sessionStatsOutput{
		SessionKey:          key,
		MessageCount:        len(messages),
		RoleCounts:          roleCounts,
		CompressedMessages:  compressedCount,
		RawTokens:           rawTokens,
		CompressedTokens:    compressedTokens,
		TokensSaved:         rawTokens - compressedTokens,
		ContextWindowTokens: contextWindow,
		UsageRatio:          usageRatio,
		WarnRatio:           warnRatio,
		PressureStatus:      status,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}
