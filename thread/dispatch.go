package thread

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/linanwx/nagobot/logger"
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
//     resume / rephrase (drop-sink semantics — any reply to caller is
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
	if t.currentSink.IsZero() && t.lastWakeSource == "" {
		return msg.CallerKindNone, "", ""
	}
	return msg.CallerKindFromSource(t.lastWakeSource), t.currentCallerKey, t.currentSink.Label
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

// SendToCaller delivers body directly to the current wake's sink —
// the same path as the default end-of-turn response delivery. Equivalent to
// "reply to whoever woke me". Suppresses the runner's end-of-turn sink delivery
// (via SetSuppressSink) so body is not double-delivered.
func (t *Thread) SendToCaller(ctx context.Context, body string) error {
	t.mu.Lock()
	sink := t.currentSink
	t.mu.Unlock()
	if sink.IsZero() {
		return fmt.Errorf("current wake has no sink (cron/heartbeat/child source)")
	}
	t.SetSuppressSink()
	return sink.Send(ctx, body)
}

// CreateOrWakeSubagent creates (or wakes existing) a subagent thread at
// {current}:threads:{taskID}. The optional agent name overrides any previously
// persisted agent on the session meta.
func (t *Thread) CreateOrWakeSubagent(_ context.Context, agentName, taskID, body string) (string, string, error) {
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
	key := parent + ":threads:" + taskID

	note, err := t.createOrWake(key, agentName, body, false, "")
	if err != nil {
		return "", "", err
	}
	return key, note, nil
}

// CreateOrWakeFork creates (or wakes existing) a fork session at
// {current}:fork:{taskID}. On new creation, the current session's history is
// copied (stripped) via session.CreateFork. Agent name overrides meta.
func (t *Thread) CreateOrWakeFork(_ context.Context, agentName, taskID, body string) (string, string, error) {
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

	note, err := t.createOrWake(key, agentName, body, true, t.sessionKey)
	if err != nil {
		return "", "", err
	}
	return key, note, nil
}

// WakeSession wakes an existing session with body as an external message.
// The wake carries a recursive paired sink: the target's reply wakes THIS
// thread's session back, and the reverse-direction wake carries another
// paired sink — so the exchange recurses until one party explicitly halts
// via dispatch({}) or redirects out via dispatch(to=user).
func (t *Thread) WakeSession(_ context.Context, sessionKey, body string) error {
	if t.mgr == nil {
		return fmt.Errorf("manager not configured")
	}
	t.mgr.Wake(sessionKey, &WakeMessage{
		Source:           WakeSession,
		Message:          body,
		Sink:             BuildPairedSessionSink(t.mgr, sessionKey, t.sessionKey),
		CallerSessionKey: t.sessionKey,
	})
	return nil
}

// buildSinkToCaller returns a recursive paired sink attached to a wake going
// from THIS thread to `targetSession`. See BuildPairedSessionSink for semantics.
func (t *Thread) buildSinkToCaller(targetSession string) Sink {
	return BuildPairedSessionSink(t.mgr, targetSession, t.sessionKey)
}

// BuildPairedSessionSink constructs a recursive session-to-session paired sink.
//
// The returned sink is attached to a wake message delivered to `selfKey`. When
// selfKey's turn emits a naive final response (no explicit dispatch), the sink
// wakes `peerKey` with that response — and that wake carries the reverse paired
// sink (selfKey ↔ peerKey swapped) so the next reply comes back to selfKey.
//
// Exchanges recurse indefinitely until one side halts explicitly:
//   - dispatch({}) — silent termination
//   - dispatch(to=user) — redirect to channel user
//   - dispatch(to=<any>) with SignalHalt — any explicit dispatch suppresses
//     the per-wake sink via SetSuppressSink
func BuildPairedSessionSink(mgr *Manager, selfKey, peerKey string) Sink {
	return Sink{
		Label: "your reply will be forwarded to caller session " + peerKey,
		Send: func(_ context.Context, response string) error {
			response = strings.TrimSpace(response)
			if response == "" {
				return nil
			}
			mgr.Wake(peerKey, &WakeMessage{
				Source:           WakeSession,
				Message:          response,
				CallerSessionKey: selfKey,
				Sink:             BuildPairedSessionSink(mgr, peerKey, selfKey),
			})
			return nil
		},
	}
}

// SendToUser delivers body via the channel user sink (this session's
// defaultSink). Only valid for user-facing sessions where defaultSink is
// the outbound channel sink.
func (t *Thread) SendToUser(ctx context.Context, body string) error {
	if !t.IsUserFacing() {
		return fmt.Errorf("session %q is not user-facing — no channel user sink", t.sessionKey)
	}
	t.mu.Lock()
	sink := t.defaultSink
	t.mu.Unlock()
	if sink.IsZero() {
		return fmt.Errorf("session %q defaultSink is unset", t.sessionKey)
	}
	return sink.Send(ctx, body)
}

// IsUserFacing reports whether this session's defaultSink is a user-channel sink
// (telegram / discord / cli / web / feishu / wecom). Subagent / fork / cron /
// heartbeat sessions return false because their defaultSink routes elsewhere
// (parent thread, wake_session target, or silent).
func (t *Thread) IsUserFacing() bool {
	key := strings.TrimSpace(t.sessionKey)
	if key == "" {
		return false
	}
	if strings.Contains(key, ":threads:") || strings.Contains(key, ":fork:") {
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

// SignalHalt marks the current turn for termination after the tool returns.
func (t *Thread) SignalHalt() {
	t.SetHaltLoop()
}

// createOrWake handles the common path for subagent/fork:
//   - session exists → optionally update meta agent, enqueue wake, return "resumed"
//   - session missing → if forkFrom != "", create fork from that source; else fresh spawn.
//     Then enqueue wake. Returns "created" or "forked-from:<src>".
func (t *Thread) createOrWake(key, agentName, body string, isFork bool, forkFrom string) (string, error) {
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
	// paired sink so the target's naive reply comes back to us and recurses
	// until one side explicitly halts.
	t.mgr.Wake(key, &WakeMessage{
		Source:           WakeSession,
		Message:          body,
		AgentName:        agentName,
		Sink:             t.buildSinkToCaller(key),
		CallerSessionKey: t.sessionKey,
	})
	return note, nil
}
