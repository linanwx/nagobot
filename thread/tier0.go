package thread

import (
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

const (
	// tier0SessionBudgetRatio is the fraction of the effective context window
	// reserved for session messages. The remaining 15% covers system prompt,
	// current user message, and output headroom.
	tier0SessionBudgetRatio = 0.85
)

// applyTier0Truncation drops messages from the head of session history
// when total tokens exceed the effective context window.
// It inserts an assistant summary marker at the truncation point.
// Returns the (possibly truncated) messages and their estimated token count.
// This is ephemeral — it does NOT modify the session file.
func (t *Thread) applyTier0Truncation(sessionMessages []provider.Message, effectiveWindow int) ([]provider.Message, int) {
	if effectiveWindow <= 0 || len(sessionMessages) == 0 {
		return sessionMessages, EstimateMessagesTokens(sessionMessages)
	}

	sessionBudget := int(float64(effectiveWindow) * tier0SessionBudgetRatio)
	if sessionBudget <= 0 {
		return sessionMessages, EstimateMessagesTokens(sessionMessages)
	}

	totalTokens := EstimateMessagesTokens(sessionMessages)
	if totalTokens <= sessionBudget {
		return sessionMessages, totalTokens
	}

	tokensToFree := totalTokens - sessionBudget
	cutIdx := findTruncationPoint(sessionMessages, tokensToFree)
	if cutIdx <= 0 || cutIdx >= len(sessionMessages) {
		return sessionMessages, totalTokens
	}

	// Collect IDs of truncated messages.
	var truncatedIDs []string
	for i := 0; i < cutIdx; i++ {
		if sessionMessages[i].ID != "" {
			truncatedIDs = append(truncatedIDs, sessionMessages[i].ID)
		}
	}

	notice := buildTier0Notice(cutIdx, len(sessionMessages), truncatedIDs)
	noticeMsg := provider.AssistantMessage(notice)

	result := make([]provider.Message, 0, 1+len(sessionMessages)-cutIdx)
	result = append(result, noticeMsg)
	result = append(result, sessionMessages[cutIdx:]...)

	resultTokens := EstimateMessagesTokens(result)

	logger.Info("tier0 truncation applied",
		"sessionKey", t.sessionKey,
		"truncated", cutIdx,
		"remaining", len(sessionMessages)-cutIdx,
		"tokensFreed", tokensToFree,
		"effectiveWindow", effectiveWindow,
	)

	return result, resultTokens
}

// findTruncationPoint returns the index of the first message to KEEP.
// It removes messages from the head until tokensToFree is satisfied,
// keeping tool_calls/tool pairs intact.
func findTruncationPoint(messages []provider.Message, tokensToFree int) int {
	freed := 0
	i := 0
	for i < len(messages) && freed < tokensToFree {
		m := messages[i]
		freed += EstimateMessageTokens(m)

		// If this is an assistant with tool_calls, skip all following tool responses too.
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			tcIDs := make(map[string]bool, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				tcIDs[tc.ID] = true
			}
			i++
			for i < len(messages) && messages[i].Role == "tool" && tcIDs[messages[i].ToolCallID] {
				freed += EstimateMessageTokens(messages[i])
				i++
			}
			continue
		}
		i++
	}

	// If we land on an orphaned tool message, skip forward.
	for i < len(messages) && messages[i].Role == "tool" {
		freed += EstimateMessageTokens(messages[i])
		i++
	}

	return i
}

// buildTier0Notice creates the truncation marker content with YAML frontmatter.
func buildTier0Notice(truncatedCount, totalCount int, truncatedIDs []string) string {
	fields := map[string]string{
		"truncated_messages": fmt.Sprintf("%d", truncatedCount),
		"total_messages":     fmt.Sprintf("%d", totalCount),
	}

	var parts []string
	parts = append(parts, fmt.Sprintf(
		"The oldest %d messages have been truncated from this session to fit within the context window.",
		truncatedCount))
	parts = append(parts, "The original session file on disk is NOT modified.")

	if len(truncatedIDs) > 0 {
		showIDs := truncatedIDs
		if len(showIDs) > 10 {
			showIDs = showIDs[:10]
		}
		parts = append(parts, fmt.Sprintf("\nTruncated message IDs: %s", strings.Join(showIDs, ", ")))
		if len(truncatedIDs) > 10 {
			parts = append(parts, fmt.Sprintf("... and %d more.", len(truncatedIDs)-10))
		}
		parts = append(parts, "\nUse `search-memory --context <message-id> --full` to retrieve truncated content if needed.")
	}

	return msg.BuildSystemMessage("tier0_truncation", fields, strings.Join(parts, "\n"))
}
