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
			Description: "Suppress output and put the current thread to sleep with a scheduled wake-up, " +
				"or mute the current turn without scheduling a wake-up (skip mode). " +
				"Use when: the message is not directed at you, someone else is talking in a group chat, " +
				"the wake timing is wrong, or you need to pause before responding. " +
				"After calling this tool, your output for this turn will be suppressed — the user will NOT receive any message.",
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
	var a sleepThreadArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	if t.sleeper == nil {
		return "Error: sleep not configured"
	}

	// Suppress sink delivery for this turn.
	t.sleeper.SetSuppressSink()

	// Skip mode: suppress output only, no scheduled wake.
	if a.Skip {
		return "Thread sleeping. Output suppressed.\n" +
			"You MUST output only \"SLEEP_OK\" and stop. Do not call any other tools or produce any other output."
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

	wakeAt := time.Now().Add(d)
	return fmt.Sprintf(
		"Sleep scheduled. Wake at %s (%s from now). Output suppressed.\n"+
			"You MUST output only \"SLEEP_OK\" and stop. Do not call any other tools or produce any other output.",
		wakeAt.Format(time.RFC3339), durationStr,
	)
}
