package thread

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

// sourceCrossThreadDispatchRequired is the `source:` frontmatter tag used on
// the system-reminder messages injected mid-turn when a cross-thread (WakeSession)
// turn produced a no-tool-calls reply. Not a WakeSource — no thread is woken
// by this tag; it appears in persisted session history (and the in-flight
// runner messages) so the model sees its rejected attempt followed by the
// requirement to use dispatch.
const sourceCrossThreadDispatchRequired = "cross-thread-dispatch-required"

// postTurnHook runs after a turn completes. Returned strings are persisted as
// user-role messages in session.jsonl and become part of subsequent turns'
// context. Parallel to turnHook (which runs before the LLM call).
type postTurnHook func(ctx context.Context, ptc postTurnContext) []string

// postTurnContext carries read-only post-turn state for hook evaluation.
// ThreadID and SessionKey are populated for logging only; hooks should make
// decisions from the remaining fields, not from identity strings.
type postTurnContext struct {
	ThreadID         string
	SessionKey       string
	WakeSource       WakeSource
	CallerSessionKey string // peer session when WakeSource == WakeSession; empty otherwise
	IsUserFacing     bool
	FinalReply       string // raw final assistant text this turn; consumed by hooks that want to surface a preview of what was forwarded
}

func (t *Thread) registerPostHook(h postTurnHook) {
	if h == nil {
		return
	}
	t.mu.Lock()
	t.postHooks = append(t.postHooks, h)
	t.mu.Unlock()
}

func (t *Thread) runPostHooks(ctx context.Context, ptc postTurnContext) []string {
	t.mu.Lock()
	if len(t.postHooks) == 0 {
		t.mu.Unlock()
		return nil
	}
	hooks := make([]postTurnHook, len(t.postHooks))
	copy(hooks, t.postHooks)
	t.mu.Unlock()

	var injected []string
	for i, h := range hooks {
		hookCtx, cancel := context.WithTimeout(ctx, hookTimeout)
		result := runSinglePostHook(hookCtx, h, ptc, i)
		cancel()
		if len(result) > 0 {
			injected = append(injected, result...)
		}
	}
	return injected
}

func runSinglePostHook(ctx context.Context, h postTurnHook, ptc postTurnContext, index int) []string {
	type hookResult struct {
		messages []string
		panicked bool
		panicVal any
	}

	ch := make(chan hookResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- hookResult{panicked: true, panicVal: r}
			}
		}()
		ch <- hookResult{messages: h(ctx, ptc)}
	}()

	select {
	case <-ctx.Done():
		logger.Warn("post-turn hook timed out, skipping",
			"hookIndex", index,
			"threadID", ptc.ThreadID,
			"sessionKey", ptc.SessionKey,
			"timeout", hookTimeout,
		)
		return nil
	case res := <-ch:
		if res.panicked {
			logger.Warn("post-turn hook panicked, skipping",
				"hookIndex", index,
				"threadID", ptc.ThreadID,
				"sessionKey", ptc.SessionKey,
				"panic", fmt.Sprintf("%v", res.panicVal),
			)
			return nil
		}
		return res.messages
	}
}

// persistPostInjections appends each non-empty payload to session.jsonl as a
// user-role message tagged with the given source. Silently skips when no
// session manager is configured. Called by RunOnce after runPostHooks.
func (t *Thread) persistPostInjections(payloads []string, source WakeSource) {
	if len(payloads) == 0 {
		return
	}
	cfg := t.cfg()
	if cfg.Sessions == nil {
		return
	}
	msgs := make([]provider.Message, 0, len(payloads))
	for _, payload := range payloads {
		if payload == "" {
			continue
		}
		pm := provider.UserMessage(payload)
		pm.Source = string(source)
		msgs = append(msgs, pm)
	}
	if len(msgs) == 0 {
		return
	}
	if err := cfg.Sessions.Append(t.sessionKey, msgs...); err != nil {
		logger.Warn("post-turn hook append failed",
			"threadID", t.id,
			"sessionKey", t.sessionKey,
			"err", err,
		)
	}
}

// buildCrossThreadDispatchRequiredPayload renders the system reminder injected
// mid-turn (via the runner's OnNoToolCalls hook) when a cross-thread
// (WakeSession) wake produced a reply with no tool calls. The reply has been
// suppressed (NOT forwarded to the peer); the model must redo the turn with
// an explicit dispatch.
func buildCrossThreadDispatchRequiredPayload(peerKey string, now time.Time) string {
	body := fmt.Sprintf(
		"Cross-thread wake (caller is session %s) requires an explicit dispatch — your prior reply was rejected and dropped, NOT forwarded to the peer. Nothing was delivered.\n\n"+
			"You must copy your last message and re-issue the turn with one of:\n"+
			"  - dispatch(sends=[{to: \"user\", body: \"...\"}]) — send to user\n"+
			"  - dispatch({}) — silently end the turn\n"+
			"  - dispatch(sends=[{to: \"session\", session_key: \"...\", body: \"...\"}]) — send to a session\n\n"+
			"Naive text content alongside cross-thread wakes is ambiguous (could mean reply-to-peer, alert-user, or both) and is no longer auto-routed; pick a target explicitly.",
		peerKey,
	)

	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "source: %s\n", sourceCrossThreadDispatchRequired)
	fmt.Fprintf(&sb, "time: %s\n", formatWakeTime(now))
	sb.WriteString("sender: system\n")
	sb.WriteString("---\n\n")
	sb.WriteString(body)
	return markInjected(sb.String())
}
