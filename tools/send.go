package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/linanwx/nagobot/provider"
)

// ChannelSender is the interface for sending messages to channels.
type ChannelSender interface {
	SendTo(ctx context.Context, channelName, text, replyTo string) error
}

// SendMessageTool sends a message to a specific channel.
type SendMessageTool struct {
	sender ChannelSender
}

// NewSendMessageTool creates a new send_message tool.
func NewSendMessageTool(sender ChannelSender) *SendMessageTool {
	return &SendMessageTool{sender: sender}
}

// Def returns the tool definition.
func (t *SendMessageTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "send_message",
			Description: "Send a message to a specific channel (e.g., telegram, cli). Use for proactively notifying users or delivering content to a channel.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel": map[string]any{
						"type":        "string",
						"description": "The target channel name (e.g., 'telegram', 'cli').",
					},
					"text": map[string]any{
						"type":        "string",
						"description": "The message text to send.",
					},
					"reply_to": map[string]any{
						"type":        "string",
						"description": "Optional: recipient or chat ID to reply to.",
					},
				},
				"required": []string{"channel", "text"},
			},
		},
	}
}

// sendMessageArgs are the arguments for send_message.
type sendMessageArgs struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
	ReplyTo string `json:"reply_to,omitempty"`
}

// Run executes the tool.
func (t *SendMessageTool) Run(ctx context.Context, args json.RawMessage) string {
	var a sendMessageArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	if t.sender == nil {
		return "Error: channel sender not configured (only available in serve mode)"
	}

	if err := t.sender.SendTo(ctx, a.Channel, a.Text, a.ReplyTo); err != nil {
		return fmt.Sprintf("Error: failed to send message: %v", err)
	}

	return fmt.Sprintf("Message sent to channel '%s'", a.Channel)
}
