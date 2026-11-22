package channel

import (
	"encoding/json"
	"net/http"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread"
)

// --- GET /api/sessions/{key...}/stats ---
//
// Context usage for one session, sized the way the compression tiers size it:
// system prompt + the session as sent + tool definitions (thread.RequestTokens).
// It used to count the session's messages alone, which read lower than the
// number that actually fires Tier 2 / Tier 3 by the whole system prompt and
// tool array — on a large-prompt session, tens of thousands of tokens.
//
// The prompt and tool sizes exist only on a loaded thread. With none loaded,
// is_runtime is false, both are 0, and the window and trim line come from the
// config default; the client says the figure is partial rather than presenting
// it as the request size.

type sessionStatsResponse struct {
	Key                string         `json:"key"`
	MessageCount       int            `json:"message_count"`
	RoleCounts         map[string]int `json:"role_counts"`
	CompressedMessages int            `json:"compressed_messages"`

	// The request, part by part. UsedTokens is their sum.
	SystemPromptTokens int `json:"system_prompt_tokens"`
	MessageTokens      int `json:"message_tokens"`
	ToolDefsTokens     int `json:"tool_defs_tokens"`
	UsedTokens         int `json:"used_tokens"`

	// What Tier-1 compression saves on the messages part.
	RawMessageTokens int `json:"raw_message_tokens"`
	TokensSaved      int `json:"tokens_saved"`

	ContextWindowTokens int     `json:"context_window_tokens"`
	UsagePercent        float64 `json:"usage_percent"`
	Tier2TriggerPercent float64 `json:"tier2_trigger_percent"`
	Tier3TriggerPercent float64 `json:"tier3_trigger_percent"`
	TrimTriggerPercent  float64 `json:"trim_trigger_percent"`
	PressureStatus      string  `json:"pressure_status"`
	IsRuntime           bool    `json:"is_runtime"`

	TokenBreakdown tokenBreakdown `json:"token_breakdown"`
}

type tokenBreakdown struct {
	BySource map[string]int `json:"by_source"`
	ByRole   map[string]int `json:"by_role"`
}

func (w *WebChannel) handleSessionStats(rw http.ResponseWriter, key string) {
	path := w.resolveSessionFile(key, session.SessionFileName)
	if path == "" {
		http.Error(rw, "invalid session key", http.StatusBadRequest)
		return
	}
	s, err := session.ReadFile(path)
	if err != nil {
		http.Error(rw, "session not found", http.StatusNotFound)
		return
	}

	messages := s.Messages
	roleCounts := map[string]int{}
	compressedCount := 0
	for _, m := range messages {
		roleCounts[m.Role]++
		if m.Compressed != "" {
			compressedCount++
		}
	}

	var usage thread.ContextUsage
	isRuntime := false
	if w.contextUsageFn != nil {
		usage, isRuntime = w.contextUsageFn(key)
	}
	if !isRuntime {
		cfg, _ := config.Load()
		if cfg == nil {
			cfg = config.DefaultConfig()
		}
		window := provider.EffectiveContextWindow(cfg.GetProvider(), cfg.GetModelName(), cfg.GetContextWindowTokens())
		tier2, tier3 := cfg.GetCompressionPercents()
		usage = thread.ContextUsage{
			ContextWindow: window,
			TrimBudget:    thread.TrimBudget(window, cfg.Thread.MaxTokens),
			Tier2Percent:  tier2,
			Tier3Percent:  tier3,
		}
	}

	sent := thread.ApplyCompressed(messages)
	messageTokens := thread.EstimateMessagesTokens(sent)
	rawMessageTokens := thread.EstimateMessagesTokens(messages)
	used := thread.RequestTokens(usage.SystemPromptTokens, usage.ToolDefsTokens, messages)

	byRole := map[string]int{}
	bySource := map[string]int{}
	for _, m := range sent {
		tok := thread.EstimateMessageTokens(m)
		byRole[m.Role] += tok
		src := m.Source
		if src == "" {
			src = "(no source)"
		}
		bySource[src] += tok
	}

	ct := thread.ComputeContextThresholds(usage.ContextWindow, usage.Tier2Percent, usage.Tier3Percent)
	resp := sessionStatsResponse{
		Key:                 key,
		MessageCount:        len(messages),
		RoleCounts:          roleCounts,
		CompressedMessages:  compressedCount,
		SystemPromptTokens:  usage.SystemPromptTokens,
		MessageTokens:       messageTokens,
		ToolDefsTokens:      usage.ToolDefsTokens,
		UsedTokens:          used,
		RawMessageTokens:    rawMessageTokens,
		TokensSaved:         rawMessageTokens - messageTokens,
		ContextWindowTokens: ct.ContextWindow,
		Tier2TriggerPercent: ct.Tier2TriggerPercent(),
		Tier3TriggerPercent: ct.Tier3TriggerPercent(),
		PressureStatus:      thread.PressureStatus(used, ct),
		IsRuntime:           isRuntime,
		TokenBreakdown:      tokenBreakdown{BySource: bySource, ByRole: byRole},
	}
	if ct.ContextWindow > 0 {
		resp.UsagePercent = float64(used) / float64(ct.ContextWindow) * 100
		resp.TrimTriggerPercent = float64(usage.TrimBudget) / float64(ct.ContextWindow) * 100
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(resp)
}
