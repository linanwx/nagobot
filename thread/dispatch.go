package thread

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/obs"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread/msg"
)

// CurrentSessionKey returns this thread's session key.
func (t *Thread) CurrentSessionKey() string {
	return t.sessionKey
}

// CallerInfo returns an atomic snapshot of the current turn's caller context
// under a single lock.
//   - kind: "user" when the wake originated from a channel user (telegram /
//     discord / cli / web / feishu / wecom), "session" when another session
//     woke us (WakeSession), "system" for cron / heartbeat / compression /
//     resume (drop-sink semantics — any reply to caller is
//     discarded). Empty string means no wake source is active (should not
//     happen mid-turn).
//   - callerKey: the upstream session key when kind=="session", empty
//     otherwise.
//   - sinkLabel: human-readable sink description (same string shown to the
//     LLM via the wake YAML `delivery` field). Included in dispatch result
//     output so the LLM can confirm where caller replies went.
func (t *Thread) CallerInfo() (kind msg.CallerKind, callerKey, sinkLabel string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.currentCallerSink.Send == nil && t.lastWakeSource == "" {
		return msg.CallerKindNone, "", ""
	}
	return msg.CallerKindFromSource(t.lastWakeSource), t.currentCallerKey, t.currentCallerSink.Label
}

// AgentExists reports whether a template with the given name is registered.
func (t *Thread) AgentExists(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	cfg := t.cfg()
	if cfg.Agents == nil {
		return false
	}
	return cfg.Agents.Def(name) != nil
}

// SessionExists reports whether a session with the given key is persisted on disk.
func (t *Thread) SessionExists(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	cfg := t.cfg()
	if cfg.Sessions == nil {
		return false
	}
	path := cfg.Sessions.PathForKey(key)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// SendToCaller delivers body to whoever woke this turn. Equivalent to "reply to
// the caller".
//
// It deliberately reads the CALLER sink, not the session set: replying to a peer
// is one message to one place, and a session's destinations are a broadcast set.
//
// It does not suppress the turn's own sinks. Suppression used to exist because a
// subagent's default sink forwarded its content to the parent, the same place
// the caller sink points, so speaking on both woke the parent twice. That
// forwarding is gone (a child's end of turn is now reported by the progress
// scanner as an event), so the two destinations never overlap.
func (t *Thread) SendToCaller(ctx context.Context, body string) error {
	t.mu.Lock()
	sink := t.currentCallerSink
	t.mu.Unlock()
	if sink.Send == nil {
		return fmt.Errorf("current wake has no caller sink (cron/heartbeat/child source)")
	}
	if err := sink.Send(ctx, body); err != nil {
		return err
	}
	t.markDispatched()
	return nil
}

// CreateOrWakeSubagent creates (or wakes existing) a subagent thread at
// {current}:threads:{taskID}. The optional agent name overrides any previously
// persisted agent on the session meta.
func (t *Thread) CreateOrWakeSubagent(ctx context.Context, agentName, taskID, body, overrideProvider, overrideModel string) (string, string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", "", fmt.Errorf("task_id is required")
	}
	if t.mgr == nil {
		return "", "", fmt.Errorf("manager not configured")
	}
	parent := t.sessionKey
	if parent == "" {
		parent = "cli"
	}
	key := parent + session.ThreadsSessionInfix + taskID

	note, err := t.createOrWake(ctx, key, agentName, body, false, "", overrideProvider, overrideModel)
	if err != nil {
		return "", "", err
	}
	return key, note, nil
}

// CreateOrWakeFork creates (or wakes existing) a fork session at
// {current}:fork:{taskID}. On new creation, the current session's history is
// copied (stripped) via session.CreateFork. Agent name overrides meta.
func (t *Thread) CreateOrWakeFork(ctx context.Context, agentName, taskID, body, overrideProvider, overrideModel string) (string, string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", "", fmt.Errorf("task_id is required")
	}
	if t.mgr == nil {
		return "", "", fmt.Errorf("manager not configured")
	}
	cfg := t.cfg()
	if cfg.Sessions == nil {
		return "", "", fmt.Errorf("session manager not configured")
	}
	parent := t.sessionKey
	if parent == "" {
		parent = "cli"
	}
	key := parent + ":fork:" + taskID

	note, err := t.createOrWake(ctx, key, agentName, body, true, t.sessionKey, overrideProvider, overrideModel)
	if err != nil {
		return "", "", err
	}
	return key, note, nil
}

// WakeSession wakes an existing session with body as an external message.
// The wake carries a recursive paired sink: the target's dispatch(to=caller:session)
// wakes THIS thread's session back, and the reverse-direction wake carries
// another paired sink, so the exchange continues until one party stops
// replying to the other (answering its own human in plain text, or ending with
// dispatch({})).
func (t *Thread) WakeSession(ctx context.Context, sessionKey, body string) error {
	if t.mgr == nil {
		return fmt.Errorf("manager not configured")
	}
	t.mgr.Wake(sessionKey, &WakeMessage{
		Source:           WakeSession,
		Message:          body,
		CallerSink:       BuildPairedSessionSink(t.mgr, sessionKey, t.sessionKey),
		CallerSessionKey: t.sessionKey,
		// The peer's turn joins OUR trace. Everything a dispatch sets in motion
		// belongs to the message that asked for it, however many sessions deep
		// it goes — that chain is the main thing a single-session log cannot
		// show.
		Traceparent: obs.Traceparent(ctx),
	})
	t.markDispatched()
	return nil
}

// buildSinkToCaller returns a recursive paired sink attached to a wake going
// from THIS thread to `targetSession`. See BuildPairedSessionSink for semantics.
func (t *Thread) buildSinkToCaller(targetSession string) SessionSink {
	return BuildPairedSessionSink(t.mgr, targetSession, t.sessionKey)
}

// BuildPairedSessionSink constructs a recursive session-to-session paired sink.
//
// The returned sink is attached as the CallerSink of a wake delivered to
// `selfKey`. When selfKey's turn calls dispatch(to=caller:session), the sink
// wakes `peerKey` with that body, and that wake carries the reverse paired sink
// (selfKey and peerKey swapped) so the next reply comes back to selfKey.
//
// Exchanges continue until one side stops replying to the other: it answers
// its own human in plain text, ends with dispatch({}), or, as a dispatched
// child, simply ends its turn (its dispatching session is then notified by the
// progress scanner's end-of-turn event, not through this sink).
func BuildPairedSessionSink(mgr *Manager, selfKey, peerKey string) SessionSink {
	return SessionSink{
		Label: "reply to caller session " + peerKey + " via dispatch(to=caller:session)",
		Send: func(ctx context.Context, response string) error {
			response = strings.TrimSpace(response)
			if response == "" {
				return nil
			}
			mgr.Wake(peerKey, &WakeMessage{
				Source:           WakeSession,
				Message:          response,
				CallerSessionKey: selfKey,
				CallerSink:       BuildPairedSessionSink(mgr, peerKey, selfKey),
				// The return leg. Without it the trace would end where the
				// child was spawned and the parent's follow-up turn — the one
				// that actually answers the human — would be an orphan root.
				Traceparent: obs.Traceparent(ctx),
			})
			return nil
		},
	}
}

// contentSink resolves where this turn's plain assistant content is delivered.
//
// Content is speech to this session's OWN human — the destination is decided by
// the server from the wake source, never by the model. This replaces the old
// dispatch(to=user): a turn reaches the human by simply producing content, and
// dispatch is left to route between agents.
//
// The turn's own set is already the answer — RunOnce assembled it as the
// session's standing destinations unioned under the channel this wake arrived
// on, so there is no longer a second set to choose between. What remains here
// is one safety gate and one bookkeeping flag.
//
// The gate: heartbeat and compression deliver nowhere. RunOnce already gives
// those sources an empty set, so this is deliberately redundant — a nightly
// maintenance turn speaking to the user is the most expensive leak this system
// has, and a second check on that path costs nothing.
//
// The bool reports a proactive delivery: content reaching a human without an
// inbound message of theirs to answer, so no origin sink's Flush writes it to
// chat.jsonl and recordProactiveChat has to.
func (t *Thread) contentSink(turnSinks SinkSet) (SinkSet, bool) {
	t.mu.Lock()
	src := t.lastWakeSource
	t.mu.Unlock()

	if isSilentSource(src) {
		return SinkSet{}, false
	}
	return turnSinks, t.IsUserFacing() && !msg.IsUserVisibleSource(src)
}

// isSilentSource reports whether a wake source produces no channel output at
// all. Such a turn gets an EMPTY sink set: there is nothing to deliver to, so
// nothing is delivered — rather than something downstream having to remember to
// suppress it. Prefix match because existing sessions carry legacy
// "heartbeat_reflect" / "heartbeat_wake" sources alongside "heartbeat".
func isSilentSource(src WakeSource) bool {
	return strings.HasPrefix(string(src), string(WakeHeartbeat)) || src == WakeCompression
}

// recordProactiveChat appends a bot-initiated message to the clean chat log.
//
// Proactive content (cron / peer session / progress) is delivered on the
// channel sink, which bypasses the per-wake chat.jsonl sink. Recording it here
// keeps the chat log — and therefore pre-think's recent-chat context — aware of
// messages the bot started on its own. The origin field records what drove the
// turn (the caller session key, or a non-user wake source like "cron") so
// readers can tell a bot-initiated message from a plain reply.
func (t *Thread) recordProactiveChat(body string) {
	if t.mgr == nil {
		return
	}
	t.mu.Lock()
	origin := t.currentCallerKey
	if origin == "" {
		origin = string(t.lastWakeSource)
	}
	t.mu.Unlock()
	dir := t.mgr.SessionDir(t.sessionKey)
	if dir == "" {
		return
	}
	if err := session.AppendChat(dir, session.ChatRoleAssistant, origin, body, time.Now()); err != nil {
		logger.Warn("chat.jsonl proactive write failed", "sessionKey", t.sessionKey, "err", err)
	}
}

// IsUserFacing reports whether this session's defaultSink is a user-channel sink
// (telegram / discord / cli / web / feishu / wecom). Subagent / fork / cron /
// heartbeat sessions return false because their defaultSink routes elsewhere
// (parent thread, wake_session target, or silent).
func (t *Thread) IsUserFacing() bool {
	return isUserFacingKey(t.sessionKey)
}

// isUserFacingKey reports whether a session key is a user-facing channel session
// (telegram / discord / cli / web / feishu / wecom) with no subagent/fork infix
// and no internal-sibling suffix.
func isUserFacingKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if strings.Contains(key, session.ThreadsSessionInfix) || strings.Contains(key, session.ForkSessionInfix) {
		return false
	}
	// Internal siblings ({key}:quote, :imagepreview, :audiopreview,
	// :progresssummary, :prethink) hang off a user-facing key but have no human
	// of their own — their entire output is a value returned to the caller via
	// OnComplete. Treating them as user-facing sends that internal artifact to
	// the channel user as proactive content: on web, where no page is ever bound
	// to a sibling key, Send falls all the way through to a broadcast push.
	if session.IsInternalSiblingSession(key) {
		return false
	}
	if strings.HasPrefix(key, "cron:") || strings.HasPrefix(key, "heartbeat") {
		return false
	}
	if key == "cli" || key == "web" {
		return true
	}
	for _, prefix := range []string{"telegram:", "discord:", "feishu:", "wecom:", "web:"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// userFacingAncestor returns the topmost ancestor of a child session key (one
// created via subagent ":threads:" or fork ":fork:") by stripping at the
// earliest child infix, plus whether that ancestor is user-facing. For a
// non-child key it returns the key unchanged.
func userFacingAncestor(key string) (string, bool) {
	key = strings.TrimSpace(key)
	ancestor := key
	ti := strings.Index(key, session.ThreadsSessionInfix)
	fi := strings.Index(key, session.ForkSessionInfix)
	cut := -1
	switch {
	case ti >= 0 && fi >= 0:
		cut = ti
		if fi < ti {
			cut = fi
		}
	case ti >= 0:
		cut = ti
	case fi >= 0:
		cut = fi
	}
	if cut >= 0 {
		ancestor = key[:cut]
	}
	return ancestor, isUserFacingKey(ancestor)
}

// SignalHalt marks the current turn for termination after the tool batch
// returns. Only dispatch({}) calls it; routing dispatches leave the turn running.
func (t *Thread) SignalHalt() {
	t.SetHaltLoop()
}

// createOrWake handles the common path for subagent/fork:
//   - session exists → optionally update meta agent, enqueue wake, return "resumed"
//   - session missing → if forkFrom != "", create fork from that source; else fresh spawn.
//     Then enqueue wake. Returns "created" or "forked-from:<src>".
func (t *Thread) createOrWake(ctx context.Context, key, agentName, body string, isFork bool, forkFrom, overrideProvider, overrideModel string) (string, error) {
	cfg := t.cfg()
	note := ""
	exists := false
	if cfg.Sessions != nil {
		if path := cfg.Sessions.PathForKey(key); path != "" {
			if _, err := os.Stat(path); err == nil {
				exists = true
			}
		}
	}

	if exists {
		// Override agent meta if explicitly specified.
		if agentName != "" && cfg.Sessions != nil {
			session.UpdateMeta(t.mgr.SessionDir(key), func(meta *session.Meta) {
				meta.Agent = agentName
			})
		}
		note = "resumed"
	} else if isFork {
		forkKey, err := cfg.Sessions.CreateFork(forkFrom, strings.TrimPrefix(key, forkFrom+":fork:"))
		if err != nil {
			return "", fmt.Errorf("fork: %w", err)
		}
		if forkKey != key {
			// Defensive: key shape must match ForkSessionInfix convention.
			logger.Warn("fork key mismatch", "expected", key, "got", forkKey)
		}
		if agentName != "" {
			session.UpdateMeta(t.mgr.SessionDir(key), func(meta *session.Meta) {
				meta.Agent = agentName
			})
		}
		note = "forked-from:" + forkFrom
	} else {
		note = "created"
	}

	// Wake the target. NewThread (inside Wake) creates the thread if needed,
	// using agentName (or falling back to meta / default). Attach a recursive
	// paired sink so the child can still message us mid-work with
	// dispatch(to=caller:session). Its result does not travel through it: when
	// the child's turn ends, the progress scanner notifies us and we read the
	// result from the child's session.
	t.mgr.Wake(key, &WakeMessage{
		Source:           WakeSession,
		Message:          body,
		AgentName:        agentName,
		OverrideProvider: overrideProvider,
		OverrideModel:    overrideModel,
		CallerSink:       t.buildSinkToCaller(key),
		CallerSessionKey: t.sessionKey,
		// Subagent and fork turns run under the trace of the message that
		// spawned them, so one trace covers the whole delegation chain.
		Traceparent: obs.Traceparent(ctx),
	})
	t.markDispatched()
	return note, nil
}

// ValidateModelOverride reports whether (providerName, model) is a usable model
// override for a subagent/fork wake: the provider must have a configured API key
// and the model must be in that provider's whitelist. Config is re-read each call
// (hot-reload) so a key added at runtime is honored. Returns a descriptive error
// — never silent — so the LLM gets actionable feedback at dispatch validation.
func (t *Thread) ValidateModelOverride(providerName, model string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("model override: cannot load config: %w", err)
	}
	if !provider.ProviderKeyAvailable(cfg, providerName) {
		return fmt.Errorf("model override: provider %q has no configured API key — run set-model --list-fallback to see usable providers", providerName)
	}
	models := provider.SupportedModelsForProvider(providerName)
	if len(models) == 0 {
		return fmt.Errorf("model override: provider %q exposes no models", providerName)
	}
	if slices.Contains(models, model) {
		return nil
	}
	return fmt.Errorf("model override: model %q is not in provider %q's whitelist (%s)", model, providerName, strings.Join(models, ", "))
}
