package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/tools"
)

// Runner is a generic agent loop executor.
type Runner struct {
	provider       provider.Provider
	tools          *tools.Registry
	metrics        *ExecMetrics              // optional; nil disables metrics collection
	onMessage      func(provider.Message)    // optional observer for intermediate messages
	onIterationEnd func() []provider.Message // optional: called after each tool iteration; returned messages are injected before the next LLM call
}

// OnMessage sets a callback invoked for each intermediate message
// (assistant-with-tools and tool results) generated during the agentic loop.
func (r *Runner) OnMessage(fn func(provider.Message)) { r.onMessage = fn }

// OnIterationEnd sets a callback invoked after each tool-call iteration
// completes, before the next LLM call. If it returns messages, they are
// appended to the conversation (e.g. mid-execution user messages).
func (r *Runner) OnIterationEnd(fn func() []provider.Message) { r.onIterationEnd = fn }

// NewRunner creates a new Runner. Pass a non-nil ExecMetrics to enable
// real-time metrics collection visible to other threads.
func NewRunner(p provider.Provider, t *tools.Registry, m *ExecMetrics) *Runner {
	return &Runner{
		provider: p,
		tools:    t,
		metrics:  m,
	}
}

// RunWithMessages executes the agent loop with pre-built messages.
func (r *Runner) RunWithMessages(ctx context.Context, messages []provider.Message) (string, error) {
	toolDefs := r.tools.Defs()

	for {
		if r.metrics != nil {
			r.metrics.mu.Lock()
			r.metrics.Iterations++
			r.metrics.CurrentTool = ""
			r.metrics.mu.Unlock()
		}

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

		assistantMsg := provider.AssistantMessageWithTools(resp.Content, resp.ReasoningContent, resp.ToolCalls)
		messages = append(messages, assistantMsg)
		if r.onMessage != nil {
			r.onMessage(assistantMsg)
		}

		for _, tc := range resp.ToolCalls {
			if r.metrics != nil {
				r.metrics.mu.Lock()
				r.metrics.CurrentTool = tc.Function.Name
				r.metrics.mu.Unlock()
			}

			start := time.Now()
			result := r.tools.Run(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			if strings.HasPrefix(result, "Error:") {
				logger.Error("tool error", "tool", tc.Function.Name, "err", result)
			}
			toolMsg := provider.ToolResultMessage(tc.ID, tc.Function.Name, result)
			messages = append(messages, toolMsg)
			if r.onMessage != nil {
				r.onMessage(toolMsg)
			}

			if r.metrics != nil {
				r.metrics.mu.Lock()
				r.metrics.TotalToolCalls++
				r.metrics.ToolCalls = append(r.metrics.ToolCalls, ToolCallRecord{
					Name:          tc.Function.Name,
					ArgsSummary:   truncateStr(tc.Function.Arguments, 200),
					ResultPreview: truncateStr(result, 200),
					DurationMs:    time.Since(start).Milliseconds(),
					Error:         strings.HasPrefix(result, "Error:"),
				})
				r.metrics.CurrentTool = ""
				r.metrics.mu.Unlock()
			}
		}

		// Inject mid-execution user messages (if any) before the next LLM call.
		if r.onIterationEnd != nil {
			if injected := r.onIterationEnd(); len(injected) > 0 {
				for _, m := range injected {
					messages = append(messages, m)
					if r.onMessage != nil {
						r.onMessage(m)
					}
				}
			}
		}
	}
}

// truncateStr returns the first n characters of s, appending "..." if truncated.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
