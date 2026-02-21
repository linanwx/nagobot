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
}

// SleepThreadTool lets the model sleep the current thread and wake it later.
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
			Description: "Sleep the current thread and schedule a delayed wake-up. " +
				"Use this when you decide not to respond now and want to be woken later. " +
				"After calling this tool, your output for this turn will be suppressed — the user will NOT receive any message.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"duration": map[string]any{
						"type":        "string",
						"description": "How long to sleep before waking (max 24h). Go duration format: \"30m\", \"2h\", \"1h30m\".",
					},
					"message": map[string]any{
						"type":        "string",
						"description": "Optional message to include when waking up. Useful for reminding yourself why you set the timer.",
					},
				},
				"required": []string{"duration"},
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
	var a sleepThreadArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	if t.sleeper == nil {
		return "Error: sleep not configured"
	}

	durationStr := strings.TrimSpace(a.Duration)
	if durationStr == "" {
		return "Error: duration is required"
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

	// Suppress sink delivery for this turn.
	t.sleeper.SetSuppressSink()

	wakeAt := time.Now().Add(d)
	return fmt.Sprintf(
		"Sleep scheduled. Wake at %s (%s from now).\n"+
			"You MUST output only \"SLEEP_OK\" and stop. Do not call any other tools or produce any other output.",
		wakeAt.Format(time.RFC3339), durationStr,
	)
}
