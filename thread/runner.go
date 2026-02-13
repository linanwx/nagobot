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
	provider provider.Provider
	tools    *tools.Registry
	metrics  *ExecMetrics // optional; nil disables metrics collection
}

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

		messages = append(messages, provider.AssistantMessageWithTools(resp.Content, resp.ReasoningContent, resp.ToolCalls))

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
			messages = append(messages, provider.ToolResultMessage(tc.ID, tc.Function.Name, result))

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
	}
}

// truncateStr returns the first n characters of s, appending "..." if truncated.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
