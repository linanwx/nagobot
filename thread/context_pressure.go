package thread

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/linanwx/nagobot/internal/runtimecfg"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

func (t *Thread) sessionFilePath() (string, bool) {
	cfg := t.cfg()
	if cfg.Sessions == nil {
		return "", false
	}
	key := strings.TrimSpace(t.sessionKey)
	if key == "" {
		return "", false
	}
	return cfg.Sessions.PathForKey(key), true
}

func (t *Thread) contextBudget() (tokens int, warnRatio float64) {
	cfg := t.cfg()
	tokens = runtimecfg.ThreadDefaultContextWindowTokens
	warnRatio = runtimecfg.ThreadDefaultContextWarnRatio

	if cfg.ContextWindowTokens > 0 {
		tokens = cfg.ContextWindowTokens
	}
	if cfg.ContextWarnRatio > 0 && cfg.ContextWarnRatio < 1 {
		warnRatio = cfg.ContextWarnRatio
	}
	return tokens, warnRatio
}

func (t *Thread) buildCompressionNotice(requestTokens, contextWindowTokens int, usageRatio float64, sessionPath string) string {
	return fmt.Sprintf(`[Context Pressure Notice]
Estimated request tokens are high for this thread.

- estimated_request_tokens: %d
- configured_context_window_tokens: %d
- estimated_usage_ratio: %.2f
- session_key: %s
- session_file: %s

After this reply, prioritize loading skill "compress_context" in the next turn and follow it to compact the session file safely. Keep critical facts, decisions, IDs, and unresolved tasks.`, requestTokens, contextWindowTokens, usageRatio, t.sessionKey, sessionPath)
}

func estimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	chars := utf8.RuneCountInString(text)
	tokens := chars / 4
	if chars%4 != 0 {
		tokens++
	}
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

func estimateMessageTokens(message provider.Message) int {
	tokens := 6 // Base per-message structure overhead.
	tokens += estimateTextTokens(message.Role)
	tokens += estimateTextTokens(message.Content)
	tokens += estimateTextTokens(message.ReasoningContent)
	tokens += estimateTextTokens(message.ToolCallID)
	tokens += estimateTextTokens(message.Name)

	for _, call := range message.ToolCalls {
		tokens += 8 // Tool call structure overhead.
		tokens += estimateTextTokens(call.ID)
		tokens += estimateTextTokens(call.Type)
		tokens += estimateTextTokens(call.Function.Name)
		tokens += estimateTextTokens(call.Function.Arguments)
	}

	return tokens
}

func estimateMessagesTokens(messages []provider.Message) int {
	total := 3 // Priming overhead.
	for _, message := range messages {
		total += estimateMessageTokens(message)
	}
	return total
}

func (t *Thread) contextPressureHook() TurnHook {
	return func(ctx TurnContext) []string {
		if strings.TrimSpace(ctx.SessionPath) == "" {
			return nil
		}
		if ctx.ContextWindowTokens <= 0 {
			return nil
		}

		threshold := int(float64(ctx.ContextWindowTokens) * ctx.ContextWarnRatio)
		if threshold <= 0 {
			threshold = ctx.ContextWindowTokens
		}
		if ctx.RequestEstimatedTokens < threshold {
			return nil
		}

		usageRatio := float64(ctx.RequestEstimatedTokens) / float64(ctx.ContextWindowTokens)
		notice := t.buildCompressionNotice(
			ctx.RequestEstimatedTokens,
			ctx.ContextWindowTokens,
			usageRatio,
			ctx.SessionPath,
		)

		logger.Info(
			"context threshold reached, compression reminder injected into current turn",
			"threadID", ctx.ThreadID,
			"sessionKey", ctx.SessionKey,
			"sessionPath", ctx.SessionPath,
			"requestEstimatedTokens", ctx.RequestEstimatedTokens,
			"contextWindowTokens", ctx.ContextWindowTokens,
			"thresholdTokens", threshold,
		)
		return []string{notice}
	}
}
