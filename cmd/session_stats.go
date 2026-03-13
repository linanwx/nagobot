package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/linanwx/nagobot/agent"
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
	SessionKey          string            `json:"session_key"`
	MessageCount        int               `json:"message_count"`
	RoleCounts          map[string]int    `json:"role_counts"`
	CompressedMessages  int               `json:"compressed_messages"`
	RoleTokens          map[string]int    `json:"role_tokens"`
	SystemPromptTokens  int               `json:"system_prompt_tokens"`
	RawTokens           int               `json:"raw_tokens"`
	CompressedTokens    int               `json:"compressed_tokens"`
	TokensSaved         int               `json:"tokens_saved"`
	ContextWindowTokens int               `json:"context_window_tokens"`
	UsageRatio          float64           `json:"usage_ratio"`
	WarnRatio           float64           `json:"warn_ratio"`
	PressureStatus      string            `json:"pressure_status"`
	LongestMessages     []longestMsgEntry `json:"longest_messages"`
}

type longestMsgEntry struct {
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	Tokens    int    `json:"tokens"`
	Chars     int    `json:"chars"`
	Snippet   string `json:"snippet"`
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

	roleTokens := map[string]int{}
	for _, m := range compressed {
		roleTokens[m.Role] += thread.EstimateMessageTokens(m)
	}

	systemPromptTokens := estimateSystemPrompt(cfg, workspace, key)

	contextWindow := cfg.GetContextWindowTokens()
	warnRatio := cfg.GetContextWarnRatio()
	usageRatio := float64(compressedTokens) / float64(contextWindow)

	status := "ok"
	if usageRatio >= warnRatio {
		status = "pressure"
	} else if usageRatio >= warnRatio*0.8 {
		status = "warning"
	}

	// Find top 3 longest messages (by token count, using compressed view).
	type indexedMsg struct {
		idx    int
		tokens int
	}
	ranked := make([]indexedMsg, len(compressed))
	for i, m := range compressed {
		ranked[i] = indexedMsg{idx: i, tokens: thread.EstimateMessageTokens(m)}
	}
	sort.Slice(ranked, func(a, b int) bool {
		return ranked[a].tokens > ranked[b].tokens
	})
	topN := 3
	if len(ranked) < topN {
		topN = len(ranked)
	}
	longest := make([]longestMsgEntry, topN)
	for i := 0; i < topN; i++ {
		m := compressed[ranked[i].idx]
		orig := messages[ranked[i].idx]
		snippet := []rune(m.Content)
		if len(snippet) > 100 {
			snippet = append(snippet[:100], []rune("...")...)
		}
		longest[i] = longestMsgEntry{
			MessageID: orig.ID,
			Role:      m.Role,
			Tokens:    ranked[i].tokens,
			Chars:     len(m.Content),
			Snippet:   string(snippet),
		}
	}

	output := sessionStatsOutput{
		SessionKey:          key,
		MessageCount:        len(messages),
		RoleCounts:          roleCounts,
		CompressedMessages:  compressedCount,
		RoleTokens:          roleTokens,
		SystemPromptTokens:  systemPromptTokens,
		RawTokens:           rawTokens,
		CompressedTokens:    compressedTokens,
		TokensSaved:         rawTokens - compressedTokens,
		ContextWindowTokens: contextWindow,
		UsageRatio:          usageRatio,
		WarnRatio:           warnRatio,
		PressureStatus:      status,
		LongestMessages:     longest,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

// estimateSystemPrompt rebuilds the agent's system prompt and estimates its token count.
// This is approximate because runtime vars (TIME, TOOLS, SKILLS, USER) are not available.
func estimateSystemPrompt(cfg *config.Config, workspace, sessionKey string) int {
	agentName := cfg.SessionAgent(sessionKey) // empty → Registry.New defaults to "soul"
	registry := agent.NewRegistry(workspace)
	a, err := registry.New(agentName)
	if err != nil {
		return 0
	}
	prompt := a.Build()
	return thread.EstimateTextTokens(prompt)
}
