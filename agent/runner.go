package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/tools"
)

// Runner is a generic agent loop executor.
// It can be used by both the main Agent and Subagents.
type Runner struct {
	provider provider.Provider
	tools    *tools.Registry
	maxIter  int
}

// NewRunner creates a new Runner.
func NewRunner(p provider.Provider, t *tools.Registry, maxIter int) *Runner {
	if maxIter <= 0 {
		maxIter = 20
	}
	return &Runner{
		provider: p,
		tools:    t,
		maxIter:  maxIter,
	}
}

// Run executes the agent loop with the given system prompt and user message.
// This is the core agent loop that can be reused by different agent types.
func (r *Runner) Run(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	messages := []provider.Message{
		provider.SystemMessage(systemPrompt),
		provider.UserMessage(userMessage),
	}

	toolDefs := r.tools.Defs()

	for i := 0; i < r.maxIter; i++ {
		// Call provider
		resp, err := r.provider.Chat(ctx, &provider.Request{
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			return "", fmt.Errorf("provider error: %w", err)
		}

		// No tool calls = done
		if !resp.HasToolCalls() {
			return resp.Content, nil
		}

		// Add assistant message with tool calls
		messages = append(messages, provider.AssistantMessageWithTools(resp.Content, resp.ToolCalls))

		// Execute tool calls
		for _, tc := range resp.ToolCalls {
			result := r.tools.Run(ctx, tc.Function.Name, tc.Arguments)
			if strings.HasPrefix(result, "Error:") {
				logger.Error(
					"tool error",
					"tool", tc.Function.Name,
					"toolCallID", tc.ID,
					"err", result,
				)
			}
			messages = append(messages, provider.ToolResultMessage(tc.ID, tc.Function.Name, result))
		}
	}

	return "", errors.New("max iterations exceeded")
}

// RunWithMessages executes the agent loop with pre-built messages.
// Useful when you need more control over the initial message context.
func (r *Runner) RunWithMessages(ctx context.Context, messages []provider.Message) (string, error) {
	toolDefs := r.tools.Defs()

	for i := 0; i < r.maxIter; i++ {
		resp, err := r.provider.Chat(ctx, &provider.Request{
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			return "", fmt.Errorf("provider error: %w", err)
		}

		if !resp.HasToolCalls() {
			return resp.Content, nil
		}

		messages = append(messages, provider.AssistantMessageWithTools(resp.Content, resp.ToolCalls))

		for _, tc := range resp.ToolCalls {
			result := r.tools.Run(ctx, tc.Function.Name, tc.Arguments)
			if strings.HasPrefix(result, "Error:") {
				logger.Error("tool error", "tool", tc.Function.Name, "err", result)
			}
			messages = append(messages, provider.ToolResultMessage(tc.ID, tc.Function.Name, result))
		}
	}

	return "", errors.New("max iterations exceeded")
}
