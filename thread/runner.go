package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
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
	toolDefsTokens int                       // cached token estimate for tool definitions
	onStream       func(streamID, delta string)      // optional: called with each streaming text delta; empty delta signals end of stream
	onMessage      func(provider.Message)            // optional: called for every message (assistant, tool, injected)
	onEvent        func(event RunnerEvent, detail string) // optional: lifecycle events (tool calls, etc.)
	onIterationEnd func() []provider.Message         // optional: called after each tool iteration; returned messages are injected before the next LLM call
	shouldHalt     func() bool                       // optional: if true, stop loop after current tool calls
	providerLabel   string             // effective provider name from last response
	modelLabel      string             // effective model name from last response
	userVisible     bool               // true when the current turn was triggered by a user-visible message
	iterations      int                // number of tool-call iterations completed
}

// RunnerEvent identifies a lifecycle event in the agentic loop.
type RunnerEvent int

const (
	// EventToolCalls fires when the current Chat() round has tool calls.
	// Detail is the name of the first tool.
	EventToolCalls RunnerEvent = iota
)

// OnStream sets a callback invoked with each streaming text delta during
// Chat(). An empty delta signals the end of the stream (Chat() returned).
// If not set, provider.Chat() runs without streaming (OnTextDelta=nil).
func (r *Runner) OnStream(fn func(streamID, delta string)) { r.onStream = fn }

// OnEvent sets a callback for lifecycle events (tool calls, etc.).
// Each event fires at most once per Chat() round.
func (r *Runner) OnEvent(fn func(event RunnerEvent, detail string)) { r.onEvent = fn }

// OnMessage sets a callback invoked for every message produced during the
// agentic loop: assistant (with or without tool calls), tool results, and
// injected messages. The caller handles persistence, delivery, and suppression.
func (r *Runner) OnMessage(fn func(provider.Message)) { r.onMessage = fn }

// OnIterationEnd sets a callback invoked after each tool-call iteration
// completes, before the next LLM call. If it returns messages, they are
// appended to the conversation (e.g. mid-execution user messages).
func (r *Runner) OnIterationEnd(fn func() []provider.Message) { r.onIterationEnd = fn }

// ShouldHalt sets a callback checked after each tool-call iteration.
// If it returns true, the loop exits immediately without calling the LLM again.
func (r *Runner) ShouldHalt(fn func() bool) { r.shouldHalt = fn }

// SetUserVisible marks this runner as handling a user-visible turn.
func (r *Runner) SetUserVisible(v bool) { r.userVisible = v }

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
		provider:       p,
		tools:          t,
		metrics:        m,
		contextBudget:  contextBudget,
		toolDefsTokens: EstimateToolDefsTokens(t.Defs()),
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

		// Build request; enable streaming only when OnStream is registered.
		chatReq := &provider.Request{
			Messages: messages,
			Tools:    toolDefs,
		}
		var streamID string
		if r.onStream != nil {
			streamID = RandomHex(8)
			chatReq.OnTextDelta = func(delta string) {
				r.onStream(streamID, delta)
			}
		}
		// Wire provider-level tool call detection for OnEvent.
		toolCallSignaled := false
		if r.onEvent != nil {
			chatReq.OnToolCallStart = func(name string) {
				if !toolCallSignaled {
					toolCallSignaled = true
					r.onEvent(EventToolCalls, name)
				}
			}
		}

		resp, err := r.provider.Chat(ctx, chatReq)

		// Signal end of stream before any other processing.
		// Empty delta tells the caller that Chat() returned and all
		// streaming deltas have been delivered.
		if r.onStream != nil {
			r.onStream(streamID, "")
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
		r.totalUsage.ReasoningTokens += resp.Usage.ReasoningTokens
		r.providerLabel = resp.ProviderLabel
		r.modelLabel = resp.ModelLabel
		if resp.Quota != nil {
			r.lastQuota = resp.Quota
		}

		// Log estimation accuracy for calibration.
		r.logEstimationAccuracy(messages, resp)

		if !resp.HasToolCalls() {
			// Emit final response via onMessage — symmetric with the tool-calls path,
			// so intermediates always contains the complete message set.
			// The caller handles delivery (streaming content was already sent via
			// OnStream; non-streaming delivery happens inside onMessage).
			if r.onMessage != nil {
				msg := provider.AssistantMessageWithTools(resp.Content, resp.ReasoningContent, resp.ReasoningDetails, nil)
				msg.ReasoningTokens = resp.Usage.ReasoningTokens
				r.onMessage(msg)
			}
			return resp.Content, nil
		}

		// Fallback: fire EventToolCalls if provider didn't signal during streaming.
		if resp.HasToolCalls() && !toolCallSignaled && r.onEvent != nil {
			r.onEvent(EventToolCalls, resp.ToolCalls[0].Function.Name)
		}

		assistantMsg := provider.AssistantMessageWithTools(resp.Content, resp.ReasoningContent, resp.ReasoningDetails, resp.ToolCalls)
		assistantMsg.ReasoningTokens = resp.Usage.ReasoningTokens
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

		r.iterations++

		// Hint: after 2 tool-call iterations in a user-visible turn,
		// nudge the model to prefer spawn_thread for remaining work.
		if r.userVisible && r.iterations == 3 {
			hint := msg.BuildSystemMessage("context_hint", nil,
				"Over 2 tool-call rounds in this turn. For tasks requiring multiple tool calls, prefer using spawn_thread to reduce main session context pressure.")
			hintMsg := provider.Message{Role: "user", Content: hint, Source: "system"}
			messages = append(messages, hintMsg)
			if r.onMessage != nil {
				r.onMessage(hintMsg)
			}
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
	total := EstimateMessagesTokens(messages) + r.toolDefsTokens
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

// logEstimationAccuracy logs the delta between our token estimation and the
// provider's actual token counts. Used for calibrating estimation accuracy.
func (r *Runner) logEstimationAccuracy(messages []provider.Message, resp *provider.Response) {
	actual := resp.Usage

	// Prompt estimation: compare our estimate vs API's actual count.
	estimatedPrompt := EstimateMessagesTokens(messages) + r.toolDefsTokens
	promptDelta := ""
	if actual.PromptTokens > 0 {
		pct := float64(estimatedPrompt-actual.PromptTokens) / float64(actual.PromptTokens) * 100
		promptDelta = fmt.Sprintf("%+.1f%%", pct)
	}

	// Reasoning estimation: use the same formula as EstimateMessageTokens
	// (tiktoken on ReasoningContent + len/3 on ReasoningDetails) so the delta
	// accurately reflects what the estimator would compute for this message.
	estimatedReasoning := 0
	if resp.ReasoningContent != "" {
		estimatedReasoning += EstimateTextTokens(resp.ReasoningContent)
	}
	if len(resp.ReasoningDetails) > 0 {
		estimatedReasoning += len(resp.ReasoningDetails) / 3
	}
	reasoningDelta := "N/A"
	if actual.ReasoningTokens > 0 && estimatedReasoning > 0 {
		pct := float64(estimatedReasoning-actual.ReasoningTokens) / float64(actual.ReasoningTokens) * 100
		reasoningDelta = fmt.Sprintf("%+.1f%%", pct)
	}

	logger.Info("token_estimate",
		"prompt_estimated", estimatedPrompt,
		"prompt_actual", actual.PromptTokens,
		"prompt_delta", promptDelta,
		"reasoning_estimated", estimatedReasoning,
		"reasoning_actual", actual.ReasoningTokens,
		"reasoning_delta", reasoningDelta,
		"completion_actual", actual.CompletionTokens,
	)
}

// truncateStr returns the first n characters of s, appending "..." if truncated.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
