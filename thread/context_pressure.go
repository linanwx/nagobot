package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

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

// Tier2TriggerPercent returns the usage percent at which Tier 2 AI compression
// starts firing (usage ≥ ContextWindow - Tier2Token). Returns 0 when
// ContextWindow is unset so callers can skip-render cleanly.
func (ct ContextThresholds) Tier2TriggerPercent() float64 {
	if ct.ContextWindow <= 0 {
		return 0
	}
	return float64(ct.ContextWindow-ct.Tier2Token) / float64(ct.ContextWindow) * 100
}

// Tier3TriggerPercent returns the usage percent at which the Tier 3 context
// pressure hook starts firing (usage ≥ ContextWindow - WarnToken). Returns 0
// when ContextWindow is unset.
func (ct ContextThresholds) Tier3TriggerPercent() float64 {
	if ct.ContextWindow <= 0 {
		return 0
	}
	return float64(ct.ContextWindow-ct.WarnToken) / float64(ct.ContextWindow) * 100
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
	if cfg.Agents != nil && t.Agent != nil {
		contextWindow = cfg.Agents.Def(t.Agent.Name).ClampContextWindow(contextWindow)
	}
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
	return provider.EstimateTextTokens(text)
}

// EstimateMessageTokens returns a tiktoken-based token estimate for a single message.
// Includes image/audio token estimation for <<media:...>> markers.
// When ReasoningTrimmed is set, reasoning is excluded (cleared at send time).
func EstimateMessageTokens(message provider.Message) int {
	tokens := 6 // Base per-message structure overhead.
	tokens += EstimateTextTokens(message.Role)
	tokens += EstimateTextTokens(message.Content)
	// Reasoning estimation: skip if trimmed (reasoning cleared at send time).
	// Always use pure estimation — no API feedback values.
	// Count ReasoningContent OR ReasoningDetails, not both — ReasoningContent
	// is extracted from ReasoningDetails for some providers (GPT-5.4 summary
	// text, Gemini thought text), so counting both double-counts.
	if !message.ReasoningTrimmed {
		if message.ReasoningContent != "" {
			tokens += EstimateTextTokens(message.ReasoningContent)
		} else if len(message.ReasoningDetails) > 0 {
			tokens += len(message.ReasoningDetails) / 3
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
	tokens += CollectMediaBreakdown([]provider.Message{message}).TotalEst()

	return tokens
}

// MediaBreakdown holds per-type media token estimates extracted from messages.
type MediaBreakdown struct {
	ImageCount int
	ImageEst   int
	AudioCount int
	AudioEst   int
	PDFCount   int
	PDFEst     int
}

// CollectMediaBreakdown scans messages for <<media:...>> markers and returns
// per-type counts and estimated tokens.
func CollectMediaBreakdown(messages []provider.Message) MediaBreakdown {
	var b MediaBreakdown
	for _, msg := range messages {
		_, markers := provider.ParseMediaMarkers(msg.Content)
		for _, m := range markers {
			if strings.HasPrefix(m.MimeType, "audio/") {
				b.AudioCount++
				b.AudioEst += provider.EstimateAudioTokens(m.FilePath)
			} else if m.MimeType == "application/pdf" {
				b.PDFCount++
				b.PDFEst += provider.EstimatePDFTokens(m.FilePath)
			} else {
				b.ImageCount++
				b.ImageEst += provider.EstimateImageTokens(m.FilePath)
			}
		}
	}
	return b
}

// TotalEst returns the sum of all media estimated tokens.
func (b MediaBreakdown) TotalEst() int { return b.ImageEst + b.AudioEst + b.PDFEst }

// HasMedia returns true if any media was found.
func (b MediaBreakdown) HasMedia() bool { return b.ImageCount+b.AudioCount+b.PDFCount > 0 }

// EstimateToolDefsTokens returns a tiktoken-based token estimate for tool definitions.
func EstimateToolDefsTokens(defs []provider.ToolDef) int {
	if len(defs) == 0 {
		return 0
	}
	data, err := json.Marshal(defs)
	if err != nil {
		return 0
	}
	return EstimateTextTokens(string(data))
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
