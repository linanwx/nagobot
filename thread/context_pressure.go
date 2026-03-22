package thread

import (
	"context"
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

// tier2Multiplier scales WarnToken to get the Tier 2 threshold.
const tier2Multiplier = 1.8

// ContextThresholds holds computed context pressure thresholds.
type ContextThresholds struct {
	ContextWindow int // effective context window (tokens)
	WarnToken     int // Tier 3: context pressure hook fires when remaining < WarnToken
	Tier2Token    int // Tier 2: AI compression fires when remaining < Tier2Token
}

// ComputeContextThresholds calculates context thresholds from contextWindow.
func ComputeContextThresholds(contextWindow int) ContextThresholds {
	if contextWindow <= 0 {
		return ContextThresholds{}
	}
	warnToken := contextWindow / 5
	if warnToken > 50000 {
		warnToken = 50000
	}
	return ContextThresholds{
		ContextWindow: contextWindow,
		WarnToken:     warnToken,
		Tier2Token:    int(float64(warnToken) * tier2Multiplier),
	}
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

func (t *Thread) contextBudget() ContextThresholds {
	cfg := t.cfg()
	_, modelName := t.resolvedProviderModel()
	contextWindow := provider.EffectiveContextWindow(modelName, cfg.ContextWindowTokens)
	return ComputeContextThresholds(contextWindow)
}

// PressureStatus returns "ok", "warning", or "pressure" based on token usage.
func PressureStatus(usedTokens int, ct ContextThresholds) string {
	if ct.ContextWindow <= 0 {
		return "ok"
	}
	remaining := ct.ContextWindow - usedTokens
	if remaining < ct.WarnToken {
		return "pressure"
	}
	if remaining < ct.Tier2Token {
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
// When ReasoningTrimmed is set, reasoning is excluded (cleared at send time).
// When ReasoningTokens is available (from provider API), it's used instead of estimation.
func EstimateMessageTokens(message provider.Message) int {
	tokens := 6 // Base per-message structure overhead.
	tokens += EstimateTextTokens(message.Role)
	tokens += EstimateTextTokens(message.Content)
	// Reasoning estimation: skip if trimmed (reasoning cleared at send time),
	// use precise API value if available, otherwise fall back to estimation.
	if !message.ReasoningTrimmed {
		if message.ReasoningTokens > 0 {
			tokens += message.ReasoningTokens
		} else {
			tokens += EstimateTextTokens(message.ReasoningContent)
			if len(message.ReasoningDetails) > 0 {
				tokens += len(message.ReasoningDetails) / 3
			}
		}
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
	return func(ctx context.Context, tc turnContext) []string {
		if strings.TrimSpace(tc.SessionPath) == "" {
			return nil
		}
		if tc.ContextWindowTokens <= 0 || tc.WarnToken <= 0 {
			return nil
		}

		threshold := tc.ContextWindowTokens - tc.WarnToken
		if tc.RequestEstimatedTokens < threshold {
			return nil
		}

		usageRatio := float64(tc.RequestEstimatedTokens) / float64(tc.ContextWindowTokens)
		notice := t.buildCompressionNotice(
			tc.RequestEstimatedTokens,
			tc.ContextWindowTokens,
			usageRatio,
			tc.SessionPath,
		)

		logger.Info(
			"context threshold reached, compression reminder injected into current turn",
			"threadID", tc.ThreadID,
			"sessionKey", tc.SessionKey,
			"sessionPath", tc.SessionPath,
			"requestEstimatedTokens", tc.RequestEstimatedTokens,
			"contextWindowTokens", tc.ContextWindowTokens,
			"thresholdTokens", threshold,
		)
		return []string{notice}
	}
}
