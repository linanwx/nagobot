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

// CallerSessionKey returns the session key of whoever woke this thread when
// addressable — i.e. a real user channel (telegram/discord/cli/web/feishu/wecom).
// System sources (cron, heartbeat, child_completed, compression, etc.) return
// empty, signalling "no routable caller".
func (t *Thread) CallerSessionKey() string {
	t.mu.Lock()
	source := t.lastWakeSource
	t.mu.Unlock()
	if msg.IsUserVisibleSource(source) {
		return t.sessionKey
	}
	return ""
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

// SendToCaller delivers body to the caller session via a wake message.
// The source is WakeExternal (a generic inter-session message) so the caller
// thread treats it as an injected system message.
func (t *Thread) SendToCaller(_ context.Context, body string) error {
	caller := t.CallerSessionKey()
	if caller == "" {
		return fmt.Errorf("no routable caller for current wake")
	}
	if t.mgr == nil {
		return fmt.Errorf("manager not configured")
	}
	t.mgr.Wake(caller, &WakeMessage{
		Source:  WakeExternal,
		Message: body,
	})
	return nil
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
func (t *Thread) WakeSession(_ context.Context, sessionKey, body string) error {
	if t.mgr == nil {
		return fmt.Errorf("manager not configured")
	}
	t.mgr.Wake(sessionKey, &WakeMessage{
		Source:  WakeExternal,
		Message: body,
	})
	return nil
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
		Source:    WakeExternal,
		Message:   body,
		AgentName: agentName,
	})
	return note, nil
}
