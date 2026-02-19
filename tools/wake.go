package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

// ThreadWaker wakes a session-bound thread with an injected message.
type ThreadWaker interface {
	Wake(sessionKey string, msg *msg.WakeMessage)
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
			Description: "Wake an existing thread by session key and inject a message for follow-up reasoning. The same wake logic is used for normal user-to-LLM messages and when scheduled jobs start or finish. Use this when you need to delegate work to another thread, control another thread's behavior, or orchestrate and manage complex multi-agent flows. Waking a thread forces it to run reasoning and may notify the user with the result, especially when that thread has a default sink.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": map[string]any{
						"type":        "string",
						"description": "Target thread session key (e.g. 'main', 'telegram:12345'). A thread is automatically created for the session if needed. The thread receives the message, runs reasoning, and may deliver its output to the session's sink. For messaging sessions like Telegram, this means the user will receive a notification.",
					},
					"message": map[string]any{
						"type":        "string",
						"description": "Message to inject into the target thread. The message is read by another LLM — write it as an instruction to that AI, not as a direct message to the end user. For example, to have a thread greet a user, pass 'Please send a warm greeting to the user' rather than sending the greeting text itself.",
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

	t.waker.Wake(sessionKey, &msg.WakeMessage{
		Source:  "user_active",
		Message: message,
	})
	return fmt.Sprintf("Thread awakened: %s", sessionKey)
}
