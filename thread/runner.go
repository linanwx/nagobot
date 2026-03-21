package thread

import (
	"context"
	"encoding/json"
	"fmt"
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
	totalUsage     provider.Usage            // accumulated usage across all Chat calls
	lastQuota      *provider.Quota           // last non-nil quota from provider response
	contextBudget  int                       // contextWindow - maxCompletionTokens; 0 = no guard
	onMessage      func(provider.Message)    // optional observer for intermediate messages
	onIterationEnd func() []provider.Message // optional: called after each tool iteration; returned messages are injected before the next LLM call
	onText          func(delta string)  // optional: called with each text chunk during streaming generation
	onChatEnd       func()             // optional: called after each provider.Chat() returns
	onFinalResponse func(string)       // optional: called with the final response content (no tool calls) before return
	shouldHalt      func() bool        // optional: if true, stop loop after current tool calls
	providerLabel   string             // effective provider name from last response
	modelLabel      string             // effective model name from last response
}

// OnMessage sets a callback invoked for each intermediate message
// (assistant-with-tools and tool results) generated during the agentic loop.
func (r *Runner) OnMessage(fn func(provider.Message)) { r.onMessage = fn }

// OnIterationEnd sets a callback invoked after each tool-call iteration
// completes, before the next LLM call. If it returns messages, they are
// appended to the conversation (e.g. mid-execution user messages).
func (r *Runner) OnIterationEnd(fn func() []provider.Message) { r.onIterationEnd = fn }

// OnText sets a callback invoked with each text delta during streaming generation.
func (r *Runner) OnText(fn func(string)) { r.onText = fn }

// OnChatEnd sets a callback invoked after each provider.Chat() call returns.
func (r *Runner) OnChatEnd(fn func()) { r.onChatEnd = fn }

// OnFinalResponse sets a callback invoked with the final response content
// (when no tool calls are present) just before RunWithMessages returns.
func (r *Runner) OnFinalResponse(fn func(string)) { r.onFinalResponse = fn }

// ShouldHalt sets a callback checked after each tool-call iteration.
// If it returns true, the loop exits immediately without calling the LLM again.
func (r *Runner) ShouldHalt(fn func() bool) { r.shouldHalt = fn }

// TotalUsage returns the accumulated token usage across all Chat calls in the loop.
func (r *Runner) TotalUsage() provider.Usage { return r.totalUsage }

// LastQuota returns the last non-nil quota snapshot from provider responses.
func (r *Runner) LastQuota() *provider.Quota { return r.lastQuota }

// ProviderLabel returns the effective provider name from the last response.
func (r *Runner) ProviderLabel() string { return r.providerLabel }

// ModelLabel returns the effective model name from the last response.
func (r *Runner) ModelLabel() string { return r.modelLabel }

// NewRunner creates a new Runner. Pass a non-nil ExecMetrics to enable
// real-time metrics collection visible to other threads.
func NewRunner(p provider.Provider, t *tools.Registry, m *ExecMetrics, contextBudget int) *Runner {
	return &Runner{
		provider:      p,
		tools:         t,
		metrics:       m,
		contextBudget: contextBudget,
	}
}

// RunWithMessages executes the agent loop with pre-built messages.
func (r *Runner) RunWithMessages(ctx context.Context, messages []provider.Message) (string, error) {
	toolDefs := r.tools.Defs()
	for {
		// Check for context cancellation before starting a new LLM call.
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		if r.metrics != nil {
			r.metrics.StartIteration()
		}

		// Guard: truncate old tool pairs if messages exceed context budget.
		if r.contextBudget > 0 {
			messages = r.trimLoopMessages(messages)
		}

		chatReq := &provider.Request{
			Messages:    messages,
			Tools:       toolDefs,
			OnTextDelta: r.onText,
		}
		resp, err := r.provider.Chat(ctx, chatReq)
		if r.onChatEnd != nil {
			r.onChatEnd()
		}
		if err != nil {
			return "", fmt.Errorf("provider error: %w", err)
		}
		// Check for context cancellation after Chat() returns — the call
		// may have succeeded but ctx was cancelled concurrently.
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		r.totalUsage.PromptTokens += resp.Usage.PromptTokens
		r.totalUsage.CompletionTokens += resp.Usage.CompletionTokens
		r.totalUsage.TotalTokens += resp.Usage.TotalTokens
		r.totalUsage.CachedTokens += resp.Usage.CachedTokens
		r.providerLabel = resp.ProviderLabel
		r.modelLabel = resp.ModelLabel
		if resp.Quota != nil {
			r.lastQuota = resp.Quota
		}

		if !resp.HasToolCalls() {
			// Emit final response via onMessage — symmetric with the tool-calls path,
			// so intermediates always contains the complete message set.
			if r.onMessage != nil {
				r.onMessage(provider.AssistantMessageWithTools(resp.Content, resp.ReasoningContent, resp.ReasoningDetails, nil))
			}
			if r.onFinalResponse != nil {
				r.onFinalResponse(resp.Content)
			}
			return resp.Content, nil
		}

		assistantMsg := provider.AssistantMessageWithTools(resp.Content, resp.ReasoningContent, resp.ReasoningDetails, resp.ToolCalls)
		messages = append(messages, assistantMsg)
		if r.onMessage != nil {
			r.onMessage(assistantMsg)
		}

		for _, tc := range resp.ToolCalls {
			if r.metrics != nil {
				r.metrics.SetCurrentTool(tc.Function.Name)
			}

			start := time.Now()
			result := r.tools.Run(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			if tools.IsToolError(result) {
				logger.Error("tool error", "tool", tc.Function.Name, "err", result)
			}
			toolMsg := provider.ToolResultMessage(tc.ID, tc.Function.Name, result)
			messages = append(messages, toolMsg)
			if r.onMessage != nil {
				r.onMessage(toolMsg)
			}

			if r.metrics != nil {
				r.metrics.RecordToolCall(ToolCallRecord{
					Name:          tc.Function.Name,
					ArgsSummary:   truncateStr(tc.Function.Arguments, 200),
					ResultPreview: truncateStr(result, 200),
					DurationMs:    time.Since(start).Milliseconds(),
					Error:         tools.IsToolError(result),
				})
			}
		}

		// A tool (e.g. sleep_thread) requested an immediate halt — stop the
		// loop without calling the LLM again.
		if r.shouldHalt != nil && r.shouldHalt() {
			return resp.Content, nil
		}

		// Inject mid-execution user messages after the latest tool results so
		// the model sees them as new context after the tool chain.
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

// trimLoopMessages removes the oldest tool-call + tool-result pairs when
// the total estimated tokens exceed contextBudget. It preserves the system
// prompt (messages[0]) and never removes the last assistant+tool group.
func (r *Runner) trimLoopMessages(messages []provider.Message) []provider.Message {
	total := EstimateMessagesTokens(messages)
	if total <= r.contextBudget {
		return messages
	}

	// Find removable tool-call/tool-result groups (skip messages[0] = system prompt).
	type group struct{ start, end int }
	var groups []group
	i := 1
	for i < len(messages) {
		m := messages[i]
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			tcIDs := make(map[string]bool, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				tcIDs[tc.ID] = true
			}
			end := i + 1
			for end < len(messages) && messages[end].Role == "tool" && tcIDs[messages[end].ToolCallID] {
				end++
			}
			groups = append(groups, group{i, end})
			i = end
			continue
		}
		i++
	}

	if len(groups) <= 1 {
		return messages // keep at least the last group
	}

	// Remove groups from the oldest until under budget, but keep the last group.
	removed := 0
	for gi := 0; gi < len(groups)-1 && total > r.contextBudget; gi++ {
		g := groups[gi]
		for j := g.start; j < g.end; j++ {
			total -= EstimateMessageTokens(messages[j])
			removed++
		}
		for j := g.start; j < g.end; j++ {
			messages[j].Role = "" // mark for removal
		}
	}

	if removed == 0 {
		return messages
	}

	// Compact: remove marked messages.
	result := make([]provider.Message, 0, len(messages)-removed)
	for _, m := range messages {
		if m.Role != "" {
			result = append(result, m)
		}
	}

	logger.Info("loop token guard: trimmed old tool groups",
		"removed", removed,
		"remainingTokens", total,
		"contextBudget", r.contextBudget,
	)

	return result
}

// truncateStr returns the first n characters of s, appending "..." if truncated.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
