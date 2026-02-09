package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/provider"
)

// ThreadWaker wakes a session-bound thread with an injected message.
type ThreadWaker interface {
	WakeWith(sessionKey, source, message string)
}

// WakeThreadTool wakes an existing thread by session key.
type WakeThreadTool struct {
	waker ThreadWaker
}

// NewWakeThreadTool creates a wake_thread tool.
func NewWakeThreadTool(waker ThreadWaker) *WakeThreadTool {
	return &WakeThreadTool{waker: waker}
}

// Def returns the tool definition.
func (t *WakeThreadTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "wake_thread",
			Description: "Wake an existing thread by session key and inject a message for follow-up reasoning.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": map[string]any{
						"type":        "string",
						"description": "Target thread session key, for example: main",
					},
					"message": map[string]any{
						"type":        "string",
						"description": "Message to inject into the target thread.",
					},
				},
				"required": []string{"session_key", "message"},
			},
		},
	}
}

type wakeThreadArgs struct {
	SessionKey string `json:"session_key"`
	Message    string `json:"message"`
}

// Run executes the tool.
func (t *WakeThreadTool) Run(ctx context.Context, args json.RawMessage) string {
	var a wakeThreadArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	if t.waker == nil {
		return "Error: thread waker not configured"
	}

	sessionKey := strings.TrimSpace(a.SessionKey)
	message := strings.TrimSpace(a.Message)
	if sessionKey == "" {
		return "Error: session_key is required"
	}
	if message == "" {
		return "Error: message is required"
	}

	t.waker.WakeWith(sessionKey, "user_active", message)
	return fmt.Sprintf("Thread awakened: %s", sessionKey)
}
