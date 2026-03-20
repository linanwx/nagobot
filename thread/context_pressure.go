package thread

import (
	"fmt"
	"strings"
	"sync"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
	"github.com/tiktoken-go/tokenizer"
)

var (
	tiktokenOnce sync.Once
	tiktokenCodec tokenizer.Codec
)

func getCodec() tokenizer.Codec {
	tiktokenOnce.Do(func() {
		enc, err := tokenizer.Get(tokenizer.O200kBase)
		if err != nil {
			logger.Warn("failed to init tiktoken codec, token estimates will be zero", "err", err)
			return
		}
		tiktokenCodec = enc
	})
	return tiktokenCodec
}

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
	_, modelName := t.resolvedProviderModel()
	return provider.EffectiveContextWindow(modelName, cfg.ContextWindowTokens), cfg.ContextWarnRatio
}

// PressureStatus returns "ok", "warning", or "pressure" based on usage ratio.
func PressureStatus(usageRatio, warnRatio float64) string {
	if usageRatio >= warnRatio {
		return "pressure"
	}
	if usageRatio >= warnRatio*0.8 {
		return "warning"
	}
	return "ok"
}

func (t *Thread) buildCompressionNotice(requestTokens, contextWindowTokens int, usageRatio float64, sessionPath string) string {
	return msg.BuildSystemMessage("context_pressure", map[string]string{
		"estimated_request_tokens":        fmt.Sprintf("%d", requestTokens),
		"configured_context_window_tokens": fmt.Sprintf("%d", contextWindowTokens),
		"estimated_usage_ratio":           fmt.Sprintf("%.2f", usageRatio),
		"session_key":                     t.sessionKey,
		"session_file":                    sessionPath,
	}, `You MUST load and execute skill "context-ops" NOW, before responding to the user. Then you can respond to the user request. Follow the skill instructions to compact the session file safely. Keep critical facts, decisions, IDs, and unresolved tasks.`)
}

// EstimateTextTokens returns a tiktoken-based token estimate for a string.
func EstimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	codec := getCodec()
	if codec == nil {
		return len(text) / 3 // rough fallback
	}
	ids, _, _ := codec.Encode(text)
	return len(ids)
}

// EstimateMessageTokens returns a tiktoken-based token estimate for a single message.
// Includes image token estimation for <<media:...>> markers.
func EstimateMessageTokens(message provider.Message) int {
	tokens := 6 // Base per-message structure overhead.
	tokens += EstimateTextTokens(message.Role)
	tokens += EstimateTextTokens(message.Content)
	tokens += EstimateTextTokens(message.ReasoningContent)
	if len(message.ReasoningDetails) > 0 {
		tokens += len(message.ReasoningDetails) / 3
	}
	tokens += EstimateTextTokens(message.ToolCallID)
	tokens += EstimateTextTokens(message.Name)

	for _, call := range message.ToolCalls {
		tokens += 8 // Tool call structure overhead.
		tokens += EstimateTextTokens(call.ID)
		tokens += EstimateTextTokens(call.Type)
		tokens += EstimateTextTokens(call.Function.Name)
		tokens += EstimateTextTokens(call.Function.Arguments)
	}

	// Estimate media tokens from <<media:mime:path>> markers.
	if _, markers := provider.ParseMediaMarkers(message.Content); len(markers) > 0 {
		for _, m := range markers {
			if strings.HasPrefix(m.MimeType, "audio/") {
				tokens += provider.EstimateAudioTokens(m.FilePath)
			} else {
				tokens += provider.EstimateImageTokens(m.FilePath)
			}
		}
	}

	return tokens
}

// EstimateMessagesTokens returns a tiktoken-based token estimate for a slice of messages.
func EstimateMessagesTokens(messages []provider.Message) int {
	total := 3 // Priming overhead.
	for _, message := range messages {
		total += EstimateMessageTokens(message)
	}
	return total
}

func (t *Thread) contextPressureHook() turnHook {
	return func(ctx turnContext) []string {
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
