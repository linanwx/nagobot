package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
	"github.com/linanwx/nagobot/tools"
)

// maxIterations caps the agent loop. If the model fails to terminate
// (no final response, no halt-requesting tool) within this many tool
// iterations, the loop aborts to prevent runaway token spend.
const maxIterations = 100

// llmCallTimeout bounds one provider round-trip. It is a floor under every
// provider, not a latency target — see callLLM.
const llmCallTimeout = 10 * time.Minute

// Runner is a generic agent loop executor.
type Runner struct {
	provider           provider.Provider
	tools              *tools.Registry
	metrics            *ExecMetrics                                        // optional; nil disables metrics collection
	totalUsage         provider.Usage                                      // accumulated usage across all Chat calls
	lastTurnUsage      provider.Usage                                      // usage from the most recent Chat call (not accumulated)
	lastQuota          *provider.Quota                                     // last non-nil quota from provider response
	contextBudget      int                                                 // contextWindow - maxCompletionTokens; 0 = no guard
	toolDefsTokens     int                                                 // cached token estimate for tool definitions
	onStream           func(streamID, delta string)                        // optional: called with each streaming text delta; empty delta signals end of stream
	onMessage          func(provider.Message)                              // optional: called for every message (assistant, tool, injected)
	onEvent            func(event RunnerEvent, detail string)              // optional: lifecycle events (tool calls, etc.)
	onIterationEnd     func() []provider.Message                           // optional: called after each tool iteration; returned messages are injected before the next LLM call
	onNoToolCalls      func(content string) []provider.Message             // optional: called when the LLM emits a final response without tool calls; returning non-empty messages rejects the finish and forces another LLM iteration with those messages appended (the assistant's rejected text is still appended/persisted so the model sees its prior attempt). Used by cross-thread wakes to require explicit dispatch.
	shouldHalt         func() bool                                         // optional: if true, stop loop after current tool calls
	onEstimationSample func(providerName, modelName string, ratio float64) // optional: called after each LLM call with the (real / estimated) total-token ratio
	providerLabel      string                                              // effective provider name from last response
	modelLabel         string                                              // effective model name from last response
	userVisible        bool                                                // true when the current turn was triggered by a user-visible message
	iterations         int                                                 // number of tool-call iterations completed
}

// RunnerEvent identifies a lifecycle event in the agentic loop.
type RunnerEvent int

const (
	// EventToolCalls fires when the current Chat() round has tool calls.
	// Detail is the name of the first tool.
	EventToolCalls RunnerEvent = iota
	// EventStreaming fires on the first text content in the current Chat() round.
	EventStreaming
)

// OnStream sets a callback invoked with each streaming text delta during
// Chat(). An empty delta signals the end of the stream (Chat() returned).
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

// OnNoToolCalls sets a callback invoked when the LLM emits a final response
// with no tool calls. It receives the assistant's text content. Returning a
// non-empty message slice rejects the finish: the assistant's text is still
// appended to history (so the model sees its rejected attempt), the returned
// messages are appended after it, and the loop continues with another LLM
// call. Returning nil or empty accepts the finish (current default).
//
// Side-effects allowed: the callback runs BEFORE the assistant message is
// emitted via OnMessage, so it may call thread methods like SetSuppressSink
// to gate sink delivery before the OnMessage handler decides whether to send.
func (r *Runner) OnNoToolCalls(fn func(content string) []provider.Message) { r.onNoToolCalls = fn }

// ShouldHalt sets a callback checked after each tool-call iteration.
// If it returns true, the loop exits immediately without calling the LLM again.
func (r *Runner) ShouldHalt(fn func() bool) { r.shouldHalt = fn }

// OnEstimationSample sets a callback fired after each LLM call with the ratio
// of (real total tokens) / (estimated total tokens) for the given
// provider+model. Used by callers to persist estimation accuracy data.
func (r *Runner) OnEstimationSample(fn func(providerName, modelName string, ratio float64)) {
	r.onEstimationSample = fn
}

// SetUserVisible marks this runner as handling a user-visible turn.
func (r *Runner) SetUserVisible(v bool) { r.userVisible = v }

// TotalUsage returns the accumulated token usage across all Chat calls in the loop.
func (r *Runner) TotalUsage() provider.Usage { return r.totalUsage }

// LastTurnUsage returns the usage from the most recent Chat call.
func (r *Runner) LastTurnUsage() provider.Usage { return r.lastTurnUsage }

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

		if r.iterations >= maxIterations {
			logger.Warn("max iterations reached, aborting agent loop", "iterations", r.iterations)
			return "", fmt.Errorf("max iterations (%d) reached without final response", maxIterations)
		}

		if r.metrics != nil {
			r.metrics.StartIteration()
		}

		// Guard: truncate old tool pairs if messages exceed context budget.
		if r.contextBudget > 0 {
			messages = r.trimLoopMessages(messages)
		}

		// Build request.
		chatReq := &provider.Request{
			Messages: messages,
			Tools:    toolDefs,
		}

		call, err := r.callLLM(ctx, chatReq)
		if err != nil {
			return "", err
		}
		resp := call.resp
		streamingSignaled := call.streamingSignaled
		toolCallSignaled := call.toolCallSignaled
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		r.lastTurnUsage = resp.Usage
		r.totalUsage.PromptTokens += resp.Usage.PromptTokens
		r.totalUsage.CompletionTokens += resp.Usage.CompletionTokens
		r.totalUsage.TotalTokens += resp.Usage.TotalTokens
		r.totalUsage.CachedTokens += resp.Usage.CachedTokens
		r.totalUsage.CacheWriteTokens += resp.Usage.CacheWriteTokens
		r.totalUsage.ReasoningTokens += resp.Usage.ReasoningTokens
		r.providerLabel = resp.ProviderLabel
		r.modelLabel = resp.ModelLabel
		if resp.Quota != nil {
			r.lastQuota = resp.Quota
		}

		// Log estimation accuracy for calibration.
		r.logEstimationAccuracy(messages, resp)

		if !resp.HasToolCalls() {
			// Fallback: fire EventStreaming for final response if not already signaled.
			if resp.Content != "" && !streamingSignaled && r.onEvent != nil {
				r.onEvent(EventStreaming, "")
			}

			// Give the caller a chance to reject this no-tool-calls finish and
			// inject system messages forcing another iteration (e.g. cross-thread
			// wakes that require an explicit dispatch). The hook may also gate
			// sink delivery before OnMessage fires (via SetSuppressSink etc).
			var rejectionMsgs []provider.Message
			if r.onNoToolCalls != nil {
				rejectionMsgs = r.onNoToolCalls(resp.Content)
			}

			// Emit final response via onMessage — symmetric with the tool-calls path,
			// so intermediates always contains the complete message set.
			// The caller handles delivery (streaming content was already sent via
			// OnStream; non-streaming delivery happens inside onMessage; rejection
			// turns rely on the hook having suppressed the sink first).
			assistantMsg := provider.AssistantMessageWithTools(resp.Content, resp.ReasoningContent, resp.ReasoningDetails, nil)
			assistantMsg.ReasoningTokens = resp.Usage.ReasoningTokens
			if r.onMessage != nil {
				r.onMessage(assistantMsg)
			}

			if len(rejectionMsgs) == 0 {
				return resp.Content, nil
			}

			// Rejection path: append assistant text + injected reminders so the
			// next LLM call sees both, and continue iterating. iterations++ keeps
			// the maxIterations cap honest in case the model keeps emitting naive
			// text instead of dispatching.
			messages = append(messages, assistantMsg)
			for _, m := range rejectionMsgs {
				messages = append(messages, m)
				if r.onMessage != nil {
					r.onMessage(m)
				}
			}
			r.iterations++
			continue
		}

		// Fallbacks: fire events if provider didn't signal during streaming.
		if resp.Content != "" && !streamingSignaled && r.onEvent != nil {
			r.onEvent(EventStreaming, "")
		}
		if resp.HasToolCalls() && !toolCallSignaled && r.onEvent != nil {
			r.onEvent(EventToolCalls, resp.ToolCalls[0].Function.Name)
		}

		// Sanitize malformed tool call arguments before persistence.
		// Some models (e.g. Qwen) occasionally produce invalid JSON in streaming.
		// Replace with "{}" so the session history stays valid; generate a
		// descriptive error result instead of executing the tool.
		invalidArgs := make(map[string]string) // tc.ID → original malformed args
		for i, tc := range resp.ToolCalls {
			if !json.Valid([]byte(tc.Function.Arguments)) {
				invalidArgs[tc.ID] = tc.Function.Arguments
				resp.ToolCalls[i].Function.Arguments = "{}"
				logger.Warn("sanitized malformed tool call arguments",
					"tool", tc.Function.Name, "original", tc.Function.Arguments)
			}
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
			var result string
			if orig, bad := invalidArgs[tc.ID]; bad {
				result = fmt.Sprintf("Error: malformed tool call arguments (invalid JSON).\nOriginal: %s\nExpected: valid JSON object for %s.", orig, tc.Function.Name)
			} else {
				toolCtx := provider.WithAssistantContent(ctx, resp.Content)
				result = r.tools.Run(toolCtx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			}
			if tools.IsToolError(result) {
				logger.Error("tool error", "tool", tc.Function.Name, "err", result)
			}
			toolMsg := provider.ToolResultMessage(tc.ID, tc.Function.Name, result)
			if yamlBlock, _, ok := SplitFrontmatter(result); ok && ExtractFrontmatterValue(yamlBlock, "skip_trim") == "true" {
				toolMsg.SkipTrim = true
			}
			messages = append(messages, toolMsg)
			if r.onMessage != nil {
				r.onMessage(toolMsg)
			}

			if r.metrics != nil {
				r.metrics.RecordToolCall(ToolCallRecord{
					Name:          tc.Function.Name,
					ArgsSummary:   truncateStr(tc.Function.Arguments, 200),
					ResultPreview: resultPreview(result),
					DurationMs:    time.Since(start).Milliseconds(),
					Error:         tools.IsToolError(result),
				})
			}
		}

		// A tool (e.g. dispatch) requested an immediate halt — stop the
		// loop without calling the LLM again.
		if r.shouldHalt != nil && r.shouldHalt() {
			return resp.Content, nil
		}

		r.iterations++

		// Hint: after 2 tool-call iterations in a user-visible turn,
		// nudge the model to delegate remaining work to a subagent via dispatch.
		if r.userVisible && r.iterations == 5 {
			hint := msg.BuildSystemMessage("context_hint", nil,
				"Over 2 tool-call rounds in this turn. For tasks requiring multiple tool calls, prefer delegating to a subagent via dispatch(to=subagent) to reduce main session context pressure.")
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

// llmCall is one completed provider round-trip: the assembled response, plus
// which lifecycle events the streaming path already fired so the caller does
// not fire them a second time.
type llmCall struct {
	resp              *provider.Response
	streamingSignaled bool
	toolCallSignaled  bool
}

// callLLM performs one provider round-trip — Chat, the delta stream, and Wait —
// under a deadline of its own.
//
// The rest of the loop needs no bound: tool execution fails by returning, and
// the loop itself is capped by maxIterations. The provider round-trip is the
// one step that can block forever, and "forever" is something a server can do
// without anything failing. On 2026-07-11 the Codex WebSocket backend accepted
// a request, sent no frame, and never closed the socket; the thread parked in
// ReadMessage and the turn simply never ended — no reply, no error, and the
// session's later messages queued behind a turn that was never coming back.
//
// The transport has since grown its own stall detection (provider.parseWSStream),
// which is faster and can retry over HTTP. This deadline is the floor beneath
// all of it: whatever a provider does, and whichever provider does it, it does
// not get to hold a thread past this. Ten minutes is deliberately far above any
// legitimate call — it exists to convert a hang into an error, not to police
// latency.
//
// Note it wraps only the round-trip. Tool execution afterwards runs on the
// caller's ctx, so a slow model cannot shorten the time a tool is given.
func (r *Runner) callLLM(ctx context.Context, chatReq *provider.Request) (*llmCall, error) {
	callCtx, cancel := context.WithTimeout(ctx, llmCallTimeout)
	defer cancel()

	result, err := r.provider.Chat(callCtx, chatReq)
	if err != nil {
		return nil, fmt.Errorf("provider error: %w", err)
	}

	out := &llmCall{}

	// Pull-based stream consumption: if the provider returned a stream,
	// consume deltas for event detection and optional sink forwarding.
	var streamID string
	if stream, ok := result.(provider.StreamChatResult); ok {
		streamID = RandomHex(8)
		var repDetector repetitionDetector
	recvLoop:
		for {
			delta, recvErr := stream.Recv()
			if recvErr == io.EOF {
				break
			}
			if recvErr != nil {
				stream.Cancel() // unblock producer goroutine
				return nil, fmt.Errorf("stream error: %w", recvErr)
			}
			switch delta.Type {
			case provider.DeltaText:
				if r.onStream != nil {
					r.onStream(streamID, delta.Text)
				}
				if !out.streamingSignaled && r.onEvent != nil {
					out.streamingSignaled = true
					r.onEvent(EventStreaming, "")
				}
				// Detect infinite repetition and cancel the stream early.
				if repDetector.feed(delta.Text) {
					logger.Warn("stream repetition detected, cancelling", "iterations", r.iterations)
					stream.Cancel()
					break recvLoop
				}
			case provider.DeltaToolCall:
				if !out.toolCallSignaled && r.onEvent != nil {
					out.toolCallSignaled = true
					r.onEvent(EventToolCalls, delta.ToolName)
				}
			}
		}
	}

	// Signal end of stream.
	if r.onStream != nil && streamID != "" {
		r.onStream(streamID, "")
	}

	resp, waitErr := result.Wait()
	if waitErr != nil {
		return nil, fmt.Errorf("provider error: %w", waitErr)
	}
	out.resp = resp
	return out, nil
}

// trimMessageGroups bounds the request to budget by halving the conversation:
// at the first crossing it drops the oldest half, and keeps dropping further
// half-budget chunks as the session grows. The drop amount is QUANTIZED to
// multiples of budget/2 — the smallest multiple that brings the remainder
// under budget. Quantization is what keeps the request head stable: a naive
// "drop until remaining ≤ total/2" recomputes a moving target every call, so
// tail growth advances the cut a little each turn, which rewrites the request
// head and kills openai-oauth's cross-turn previous_response_id delta (and
// every provider's prefix cache). Measured 2026-07-12 (coffee channel,
// gpt-5.6-terra): one Tier-2 turn made 4 calls of ~175K prompt tokens each at
// ~0% cache hit (~44 credits) because the head moved before each call. With
// quantized drops the head moves once per budget/2 (~93K on terra) of growth,
// and each move frees another ~budget/2 of headroom.
//
// Boundaries: messages[0] (system prompt) is always kept; the cut lands on a
// user-message boundary, so tool results are never orphaned from their calls
// and the message after the system prompt is always a user message; the final
// turn (last user message onward) is never prefix-cut. If the drop quota is
// not reached by whole turns alone (one mega-agentic turn — the in-loop
// guard's case), it falls back to dropping that turn's oldest
// assistant+tool_call groups, keeping the turn's user message and the most
// recent group. May still return over budget when nothing droppable remains —
// that is logged, never silent. Ephemeral — never modifies the session file.
// Shared by the turn-start trim and the in-loop guard.
func trimMessageGroups(messages []provider.Message, toolDefsTokens, budget int) []provider.Message {
	if budget <= 0 || len(messages) == 0 {
		return messages
	}
	total := EstimateMessagesTokens(messages) + toolDefsTokens
	if total <= budget {
		return messages
	}

	// Drop quota: smallest multiple of budget/2 that brings total under
	// budget. A step function of total — stable while the session grows
	// within one half-budget band, which is what pins the cut in place.
	half := max(budget/2, 1)
	dropTarget := (total - budget + half - 1) / half * half

	// Stage 1: drop whole turns from the head. Advance the cut user-boundary
	// by user-boundary until the dropped prefix meets the quota, never cutting
	// into the final turn (the current wake and its agentic tail).
	lastUser := -1
	for i := len(messages) - 1; i >= 1; i-- {
		if messages[i].Role == "user" {
			lastUser = i
			break
		}
	}
	result := messages
	dropped := 0
	removedTurnMsgs := 0
	if lastUser > 1 {
		keepFrom := 1
		prefix := 0 // tokens in messages[1:i]
		for i := 1; i <= lastUser; i++ {
			if messages[i].Role == "user" {
				keepFrom = i
				if prefix >= dropTarget {
					break
				}
			}
			prefix += EstimateMessageTokens(messages[i])
		}
		if keepFrom > 1 {
			result = make([]provider.Message, 0, 1+len(messages)-keepFrom)
			result = append(result, messages[0])
			result = append(result, messages[keepFrom:]...)
			removedTurnMsgs = keepFrom - 1
			before := total
			total = EstimateMessagesTokens(result) + toolDefsTokens
			dropped = before - total
		}
	}

	// Stage 2 (rare): whole turns didn't meet the quota — the final turn alone
	// blows the budget. Drop its oldest assistant+tool_call groups, keeping
	// the most recent group. Same quantized quota, so the same stability.
	removedGroupMsgs := 0
	if dropped < dropTarget {
		type group struct{ start, end int }
		var groups []group
		i := 1
		for i < len(result) {
			m := result[i]
			if m.Role == "assistant" && len(m.ToolCalls) > 0 {
				tcIDs := make(map[string]bool, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					tcIDs[tc.ID] = true
				}
				end := i + 1
				for end < len(result) && result[end].Role == "tool" && tcIDs[result[end].ToolCallID] {
					end++
				}
				groups = append(groups, group{i, end})
				i = end
				continue
			}
			i++
		}
		if len(groups) > 1 {
			marked := append([]provider.Message(nil), result...)
			for gi := 0; gi < len(groups)-1 && dropped < dropTarget; gi++ {
				g := groups[gi]
				for j := g.start; j < g.end; j++ {
					n := EstimateMessageTokens(marked[j])
					total -= n
					dropped += n
					marked[j].Role = "" // mark for removal
					removedGroupMsgs++
				}
			}
			if removedGroupMsgs > 0 {
				compact := make([]provider.Message, 0, len(marked)-removedGroupMsgs)
				for _, m := range marked {
					if m.Role != "" {
						compact = append(compact, m)
					}
				}
				result = compact
			}
		}
	}

	if removedTurnMsgs+removedGroupMsgs == 0 {
		logger.Warn("context budget guard: over budget but nothing droppable",
			"tokens", total,
			"budget", budget,
		)
		return messages
	}

	logger.Info("context budget guard: halved conversation",
		"removedTurnMsgs", removedTurnMsgs,
		"removedGroupMsgs", removedGroupMsgs,
		"remainingTokens", total,
		"budget", budget,
		"dropTarget", dropTarget,
	)
	if total > budget {
		logger.Warn("context budget guard: still over budget after trim",
			"tokens", total,
			"budget", budget,
		)
	}

	return result
}

// contextLoopBudget is the shared token budget for in-context trimming — the
// LAST-RESORT line, which must sit above both compression tiers (Tier 2 at 70%,
// Tier 3 at 85% of the window) so compression always gets the first chance:
//
//	min(92% of the window, window − maxCompletion − 10K)
//
// The 92% term leaves 8% proportional slack for token-estimate error (measured
// ~6% overshoot); the absolute term guarantees request validity on providers
// that send max_tokens, and only binds on small windows (< ~330K at the 16,384
// default). The ordering trim > Tier 3 holds for windows ≥ ~176K; below that
// the output reservation dominates and the fix is lowering maxTokens, not
// moving the tiers. Returns 0 when non-positive.
func contextLoopBudget(contextWindow, maxCompletion int) int {
	b := min(contextWindow*92/100, contextWindow-maxCompletion-10000)
	if b < 0 {
		return 0
	}
	return b
}

// trimLoopMessages bounds the in-loop context to the shared budget.
func (r *Runner) trimLoopMessages(messages []provider.Message) []provider.Message {
	return trimMessageGroups(messages, r.toolDefsTokens, r.contextBudget)
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

	// Reasoning estimation: mirrors EstimateMessageTokens — count
	// ReasoningContent OR ReasoningDetails, not both (avoids double-counting).
	estimatedReasoning := 0
	if resp.ReasoningContent != "" {
		estimatedReasoning += EstimateTextTokens(resp.ReasoningContent)
	} else if len(resp.ReasoningDetails) > 0 {
		estimatedReasoning += len(resp.ReasoningDetails) / 3
	}
	reasoningDelta := "N/A"
	if actual.ReasoningTokens > 0 && estimatedReasoning > 0 {
		pct := float64(estimatedReasoning-actual.ReasoningTokens) / float64(actual.ReasoningTokens) * 100
		reasoningDelta = fmt.Sprintf("%+.1f%%", pct)
	}

	// Completion estimation: response text plus reasoning. Mirrors what
	// EstimateMessageTokens would count if the response were re-fed as a message.
	estimatedCompletion := EstimateTextTokens(resp.Content) + estimatedReasoning

	var media MediaBreakdown
	if r.metrics != nil {
		r.metrics.PromptEstimated = estimatedPrompt
		r.metrics.CompletionEstimated = estimatedCompletion
		r.metrics.ReasoningEstimated = estimatedReasoning
		r.metrics.LastPromptActual = actual.PromptTokens
		r.metrics.LastCompletionActual = actual.CompletionTokens
		r.metrics.LastTotalActual = actual.TotalTokens
		r.metrics.LastCachedActual = actual.CachedTokens
		r.metrics.LastCacheWriteActual = actual.CacheWriteTokens
		r.metrics.LastReasoningActual = actual.ReasoningTokens
		media = r.metrics.Media
	}

	// Fire the per-call estimation sample. Caller decides whether to persist.
	if r.onEstimationSample != nil {
		estTotal := estimatedPrompt + estimatedCompletion
		actualTotal := actual.PromptTokens + actual.CompletionTokens
		if estTotal > 0 && actualTotal > 0 {
			ratio := float64(actualTotal) / float64(estTotal)
			r.onEstimationSample(resp.ProviderLabel, resp.ModelLabel, ratio)
		}
	}

	fields := []any{
		"prompt_estimated", estimatedPrompt,
		"prompt_actual", actual.PromptTokens,
		"prompt_delta", promptDelta,
		"reasoning_estimated", estimatedReasoning,
		"reasoning_actual", actual.ReasoningTokens,
		"reasoning_delta", reasoningDelta,
		"completion_actual", actual.CompletionTokens,
	}
	if media.HasMedia() {
		fields = append(fields,
			"image_count", media.ImageCount,
			"image_est", media.ImageEst,
			"audio_count", media.AudioCount,
			"audio_est", media.AudioEst,
			"pdf_count", media.PDFCount,
			"pdf_est", media.PDFEst,
			"media_est_total", media.TotalEst(),
		)
	}
	logger.Info("token_estimate", fields...)
}

// resultPreview returns a ≤200-char preview of a tool result, skipping any
// leading YAML frontmatter so the preview shows actual content rather than the
// metadata header (status/time/source) most tool results lead with.
func resultPreview(result string) string {
	src := result
	if _, body, ok := SplitFrontmatter(result); ok {
		src = strings.TrimSpace(body)
	}
	return truncateStr(src, 200)
}

// truncateStr returns the first n characters of s, appending "..." if truncated.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// repetitionDetector tracks streaming text and detects infinite repetition.
// It accumulates text in a buffer and periodically checks whether a substring
// of 20-100 runes repeats 10+ times consecutively. Zero value is ready to use.
type repetitionDetector struct {
	buf       strings.Builder
	nextCheck int // byte length threshold for next check
}

// feed appends delta text and returns true if repetition is detected.
func (d *repetitionDetector) feed(text string) bool {
	d.buf.WriteString(text)
	n := d.buf.Len()
	// Only check every 500 bytes to avoid per-delta overhead.
	if n < 1000 || n < d.nextCheck {
		return false
	}
	d.nextCheck = n + 500

	runes := []rune(d.buf.String())
	rn := len(runes)
	const minPat = 20
	const maxPat = 100
	const threshold = 10

	// Check from the end of accumulated text — repetition is at the tail.
	for patLen := minPat; patLen <= maxPat && patLen <= rn/threshold; patLen++ {
		// Take the last patLen runes as the candidate pattern.
		pat := runes[rn-patLen:]
		count := 1
		pos := rn - patLen*2
		for pos >= 0 {
			if !runesEqual(runes[pos:pos+patLen], pat) {
				break
			}
			count++
			pos -= patLen
		}
		if count >= threshold {
			return true
		}
	}
	return false
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
