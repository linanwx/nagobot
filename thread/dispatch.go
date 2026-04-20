package thread

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
)

// CurrentSessionKey returns this thread's session key.
func (t *Thread) CurrentSessionKey() string {
	return t.sessionKey
}

// CallerSessionKey returns an informational tag identifying the caller, when
// the current wake has a routable sink. Returns empty if no sink is active
// (e.g. cron, heartbeat, compression, or child_completed with nil sink).
// The actual delivery target is the sink itself; the returned key is just
// context for dispatch result formatting.
func (t *Thread) CallerSessionKey() string {
	t.mu.Lock()
	hasSink := !t.currentSink.IsZero()
	t.mu.Unlock()
	if !hasSink {
		return ""
	}
	return t.sessionKey
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
// The wake carries a per-wake sink that routes the target's reply back to
// THIS thread's session — so the target's `dispatch(to=caller)` returns to us,
// not to the target's own channel user.
func (t *Thread) WakeSession(_ context.Context, sessionKey, body string) error {
	if t.mgr == nil {
		return fmt.Errorf("manager not configured")
	}
	sinkToWaker := t.buildSinkToCaller(sessionKey)
	t.mgr.Wake(sessionKey, &WakeMessage{
		Source:           WakeSession,
		Message:          body,
		Sink:             sinkToWaker,
		CallerSessionKey: t.sessionKey,
	})
	return nil
}

// buildSinkToCaller constructs a sink that wakes THIS thread's session with the
// target's reply. Used as the per-wake sink when this thread dispatches
// to=session at another session.
func (t *Thread) buildSinkToCaller(targetSession string) Sink {
	wakerKey := t.sessionKey
	mgr := t.mgr
	return Sink{
		Label: "your reply will be forwarded to caller session " + wakerKey,
		Send: func(_ context.Context, response string) error {
			response = strings.TrimSpace(response)
			if response == "" {
				return nil
			}
			mgr.Wake(wakerKey, &WakeMessage{
				Source:           WakeSession,
				Message:          response,
				CallerSessionKey: targetSession,
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
	// using agentName (or falling back to meta / default).
	t.mgr.Wake(key, &WakeMessage{
		Source:           WakeSession,
		Message:          body,
		AgentName:        agentName,
		CallerSessionKey: t.sessionKey,
	})
	return note, nil
}
