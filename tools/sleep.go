package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/provider"
)

// ThreadSleeper schedules a persistent delayed wake for the current thread.
type ThreadSleeper interface {
	SleepThread(duration time.Duration, message string) error
	SetSuppressSink()
	SetHaltLoop()
}

// SleepThreadTool lets the model sleep the current thread.
type SleepThreadTool struct {
	sleeper ThreadSleeper
}

// NewSleepThreadTool creates a new sleep_thread tool.
func NewSleepThreadTool(sleeper ThreadSleeper) *SleepThreadTool {
	return &SleepThreadTool{sleeper: sleeper}
}

// Def returns the tool definition with full parameters (duration/message/skip).
func (t *SleepThreadTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name: "sleep_thread",
			Description: "Suppress output for the CURRENT turn and optionally schedule a future wake-up. " +
				"Two modes: (1) sleep mode (default) — schedules an extra cron wake-up after the specified duration; " +
				"(2) skip mode (skip=true) — suppresses output only, no wake-up scheduled. " +
				"Use when: the message is not directed at you, someone else is talking in a group chat, " +
				"the wake timing is wrong, or you need to pause before responding. " +
				"IMPORTANT: Only the current turn is suppressed. Future wakes from heartbeat, cron, or user messages " +
				"are NOT affected — they still fire on their normal schedule. " +
				"Sleep mode does NOT block other wakes; it only adds one extra wake-up at the specified time.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"duration": map[string]any{
						"type":        "string",
						"description": "How long to sleep before waking (max 24h). Go duration format: \"30m\", \"2h\", \"1h30m\". Defaults to \"2m\" if omitted.",
					},
					"message": map[string]any{
						"type":        "string",
						"description": "Optional memo to include when waking up, reminding yourself why you slept.",
					},
					"skip": map[string]any{
						"type":        "boolean",
						"description": "Set to true to suppress output without scheduling a wake-up. Use when the message is not directed at you, someone else is talking, or the wake timing is wrong.",
					},
				},
			},
		},
	}
}

type sleepThreadArgs struct {
	Duration string `json:"duration"`
	Message  string `json:"message"`
	Skip     bool   `json:"skip"`
}

// Run executes the tool.
func (t *SleepThreadTool) Run(_ context.Context, args json.RawMessage) string {
	if t.sleeper == nil {
		return "Error: sleep not configured"
	}

	var a sleepThreadArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	// Suppress sink delivery for this turn.
	t.sleeper.SetSuppressSink()

	// Skip mode: suppress output only, no scheduled wake.
	if a.Skip {
		t.sleeper.SetHaltLoop()
		return toolResult("sleep_thread", map[string]any{
			"mode": "skip",
		}, "WARNING: This tool call terminates the current reasoning loop immediately. "+
			"No wake scheduled. All output for this turn is suppressed — the user will NOT receive any message. "+
			"This does NOT affect future turns — heartbeat, cron, and user messages will still trigger normally.")
	}

	// Default duration: 2 minutes.
	durationStr := strings.TrimSpace(a.Duration)
	if durationStr == "" {
		durationStr = "2m"
	}

	d, err := time.ParseDuration(durationStr)
	if err != nil {
		return fmt.Sprintf("Error: invalid duration %q: %v", durationStr, err)
	}
	if d <= 0 {
		return "Error: duration must be positive"
	}
	if d > 24*time.Hour {
		return "Error: duration must not exceed 24h"
	}

	message := strings.TrimSpace(a.Message)
	if message == "" {
		message = "Sleep timer expired."
	}

	if err := t.sleeper.SleepThread(d, message); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	t.sleeper.SetHaltLoop()
	wakeAt := time.Now().Add(d)
	return toolResult("sleep_thread", map[string]any{
		"mode":      "sleep",
		"mechanism": "cron_scheduler",
		"wake_at":   wakeAt.Format(time.RFC3339),
		"duration":  durationStr,
		"message":   message,
	}, "WARNING: This tool call terminates the current reasoning loop immediately. "+
		"All output for this turn is suppressed — the user will NOT receive any message. "+
		"The cron scheduler will wake this thread at the specified time. "+
		"This does NOT affect future turns — heartbeat, cron, and user messages will still trigger normally.")
}

// HeartbeatSleepTool is the sleep_thread replacement used during heartbeat turns.
// No parameters — just terminate the turn and suppress output.
type HeartbeatSleepTool struct {
	suppressor HaltSuppressor
}

// HaltSuppressor is the minimal interface for heartbeat sleep: suppress + halt.
type HaltSuppressor interface {
	SetSuppressSink()
	SetHaltLoop()
}

// NewHeartbeatSleepTool creates a heartbeat-only sleep_thread tool.
func NewHeartbeatSleepTool(s HaltSuppressor) *HeartbeatSleepTool {
	return &HeartbeatSleepTool{suppressor: s}
}

// Def returns the tool definition — no parameters, heartbeat-specific description.
func (t *HeartbeatSleepTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name: "sleep_thread",
			Description: "End this heartbeat turn silently. Suppresses all output — the user will not see this turn. " +
				"The heartbeat scheduler fires the next pulse automatically. " +
				"Call with no arguments.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

// Run terminates the heartbeat turn. All arguments are ignored.
func (t *HeartbeatSleepTool) Run(_ context.Context, _ json.RawMessage) string {
	t.suppressor.SetSuppressSink()
	t.suppressor.SetHaltLoop()
	return toolResult("sleep_thread", map[string]any{
		"mode": "heartbeat_terminate",
	}, "Heartbeat turn terminated. Output suppressed. "+
		"The heartbeat scheduler will fire the next pulse automatically.")
}
