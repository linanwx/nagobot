package thread

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

// sourceImplicitCallerForward is the `source:` frontmatter tag used on the
// system-reminder messages this package appends after an implicit caller
// forward. Not a WakeSource — no thread is woken by this tag; it only appears
// in persisted session history as an audit crumb.
const sourceImplicitCallerForward = "implicit-caller-forward"

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
	SinkSuppressed   bool // true when an explicit dispatch suppressed default sink delivery
	ResponseNonEmpty bool // true when the Runner actually produced naive text delivered via sink
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

// implicitCallerForwardHook records an outbound breadcrumb whenever a turn
// ended with naive final text being implicitly routed back to the waking peer
// session via the paired caller sink. The explicit dispatch(to=caller:session)
// path already leaves a tool result documenting the routing; the implicit path
// does not, leaving subsequent turns unable to see that the text left the
// session.
func (t *Thread) implicitCallerForwardHook() postTurnHook {
	return func(_ context.Context, ptc postTurnContext) []string {
		if ptc.WakeSource != WakeSession {
			return nil
		}
		if ptc.CallerSessionKey == "" {
			return nil
		}
		if ptc.SinkSuppressed {
			return nil
		}
		if !ptc.ResponseNonEmpty {
			return nil
		}
		return []string{buildImplicitCallerForwardPayload(ptc.CallerSessionKey, ptc.IsUserFacing, time.Now().In(t.location()))}
	}
}

// buildImplicitCallerForwardPayload renders the system-reminder payload that
// gets appended to session.jsonl after an implicit caller-forward turn.
func buildImplicitCallerForwardPayload(peerKey string, isUserFacing bool, now time.Time) string {
	body := "Default output reply detected. Replied to the caller — your reply has been forwarded to caller session " + peerKey + ". Nothing else."
	if isUserFacing {
		body += " May also dispatch to user next time if you want to also let the user know this."
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "source: %s\n", sourceImplicitCallerForward)
	fmt.Fprintf(&sb, "time: %s\n", formatWakeTime(now))
	sb.WriteString("sender: system\n")
	sb.WriteString("---\n\n")
	sb.WriteString(body)
	return markInjected(sb.String())
}
