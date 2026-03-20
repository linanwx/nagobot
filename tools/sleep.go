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
	IsHeartbeatWake() bool
}

// SleepThreadTool lets the model sleep the current thread.
type SleepThreadTool struct {
	sleeper ThreadSleeper
}

// NewSleepThreadTool creates a new sleep_thread tool.
func NewSleepThreadTool(sleeper ThreadSleeper) *SleepThreadTool {
	return &SleepThreadTool{sleeper: sleeper}
}

// Def returns the tool definition.
func (t *SleepThreadTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name: "sleep_thread",
			Description: "End the current turn silently. All output is suppressed — the user will not see this turn. " +
				"Optionally schedule a one-time wake-up after a specified duration. " +
				"Call with no arguments to simply end the turn. " +
				"IMPORTANT: Only the current turn is suppressed. Future wakes from heartbeat, cron, or user messages " +
				"are NOT affected — they still fire on their normal schedule.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"duration": map[string]any{
						"type":        "string",
						"description": "How long to sleep before waking (max 24h). Go duration format: \"30m\", \"2h\", \"1h30m\". If omitted, no wake-up is scheduled.",
					},
					"message": map[string]any{
						"type":        "string",
						"description": "Optional memo to include when waking up, reminding yourself why you slept. Only used when duration is set.",
					},
				},
			},
		},
	}
}

type sleepThreadArgs struct {
	Duration string `json:"duration"`
	Message  string `json:"message"`
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

	durationStr := strings.TrimSpace(a.Duration)

	// No duration: just end the turn silently.
	if durationStr == "" {
		t.sleeper.SetHaltLoop()
		return toolResult("sleep_thread", map[string]any{
			"mode": "silent_end",
		}, "Turn ended silently. No wake scheduled. "+
			"Future heartbeat, cron, and user messages still fire on their normal schedule.")
	}

	// Duration provided: schedule a wake-up.
	// Reject during heartbeat turns — heartbeat scheduler handles its own timing.
	if t.sleeper.IsHeartbeatWake() {
		return "Error: duration parameter is not allowed during heartbeat turns. " +
			"Call sleep_thread with no arguments to end the turn. " +
			"The heartbeat scheduler manages wake timing automatically."
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
		"mode":     "sleep",
		"wake_at":  wakeAt.Format(time.RFC3339),
		"duration": durationStr,
		"message":  message,
	}, "Turn ended silently. Wake scheduled at the specified time. "+
		"Future heartbeat, cron, and user messages still fire on their normal schedule.")
}
