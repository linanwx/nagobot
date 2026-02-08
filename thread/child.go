package thread

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/logger"
)

// SpawnChild spawns a child thread for delegated work.
func (t *Thread) SpawnChild(ctx context.Context, ag *agent.Agent, task, taskContext string, wait bool) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("task is required")
	}

	if ag == nil {
		t.mu.Lock()
		ag = t.agent
		t.mu.Unlock()
	}
	if ag == nil {
		return "", fmt.Errorf("child agent is not configured")
	}

	childAgent := WrapAgentTaskPlaceholder(ag, task)
	childSessionKey := ""
	if t.cfg != nil && t.cfg.Sessions != nil {
		parentIdentity := strings.TrimSpace(t.sessionKey)
		if parentIdentity == "" {
			parentIdentity = strings.TrimSpace(t.id)
		}
		childSessionKey = generateChildSessionKey(parentIdentity)
	}
	child := newChildWithSession(t.cfg, childAgent, nil, childSessionKey)

	userMessage := task
	if strings.TrimSpace(taskContext) != "" {
		userMessage = fmt.Sprintf("%s\n\nContext:\n%s", task, taskContext)
	}

	if wait {
		return child.Wake(ctx, "child_task", userMessage)
	}

	t.mu.Lock()
	t.childCounter++
	childID := fmt.Sprintf("%s-child-%d", t.id, t.childCounter)
	state := &childState{
		done: make(chan struct{}),
	}
	t.children[childID] = state
	t.mu.Unlock()

	go func() {
		result, err := child.Wake(ctx, "child_task", userMessage)
		t.mu.Lock()
		state.result = result
		state.err = err
		close(state.done)
		t.mu.Unlock()

		var message string
		if err != nil {
			message = fmt.Sprintf("Child %s failed: %v", childID, err)
		} else {
			message = fmt.Sprintf("Child %s completed:\n%s", childID, result)
		}
		t.WakeAsync(ctx, "child_completed", message)
	}()

	return childID, nil
}

// GetChild returns child thread status and result.
func (t *Thread) GetChild(childID string) (status, result string, err error) {
	t.mu.Lock()
	state, ok := t.children[childID]
	if !ok {
		t.mu.Unlock()
		return "", "", fmt.Errorf("child thread not found: %s", childID)
	}

	select {
	case <-state.done:
		result = state.result
		err = state.err
		t.mu.Unlock()
		if err != nil {
			return "failed", "", err
		}
		return "completed", result, nil
	default:
		t.mu.Unlock()
		return "running", "", nil
	}
}

func generateChildSessionKey(parentIdentity string) string {
	parentIdentity = strings.TrimSpace(parentIdentity)
	if parentIdentity == "" {
		parentIdentity = "main"
	}

	now := time.Now().UTC()
	datePart := now.Format("2006-01-02")
	timePart := now.Format("20060102T150405Z")
	if suffix := RandomHex(4); suffix != "" {
		return fmt.Sprintf("%s:threads:%s:%s-%s", parentIdentity, datePart, timePart, suffix)
	}
	return fmt.Sprintf("%s:threads:%s:%d", parentIdentity, datePart, now.UnixNano())
}

// RandomHex returns a random lowercase hex string of length n*2.
func RandomHex(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

// Wake executes one turn from a wake source.
func (t *Thread) Wake(ctx context.Context, source, message string) (string, error) {
	source = strings.TrimSpace(source)
	message = strings.TrimSpace(message)
	if message == "" {
		return "", nil
	}
	if source == "" {
		source = "unknown"
	}

	now := time.Now()
	wakeHeader := fmt.Sprintf(
		"[Wake reason: %s | %s (%s, %s, UTC%s)]",
		source,
		now.Format(time.RFC3339),
		now.Weekday().String(),
		now.Location().String(),
		now.Format("-07:00"),
	)

	action := wakeActionHint(source)
	if action == "" {
		return t.run(ctx, wakeHeader+"\n"+message)
	}

	return t.run(ctx, wakeHeader+"\n[Wake Action]\n"+action+"\n\n"+message)
}

func wakeActionHint(source string) string {
	switch source {
	case "user_message":
		return "Respond directly to the user request."
	case "user_active":
		return "Resume the target session and respond to this wake message."
	case "child_task":
		return "Execute this delegated task and return a concise, actionable result."
	case "child_completed":
		return "A child thread completed. Summarize the result and report to the user."
	case "cron":
		return "A scheduled cron task has started. Execute it based on the provided job context."
	case "cron_finished":
		return "A cron task has finished. Summarize the result and report to the user."
	case "external":
		return "Process this external wake message and continue the session."
	default:
		return "Process this wake message and continue."
	}
}

// WakeAsync executes a wake-triggered run asynchronously.
func (t *Thread) WakeAsync(ctx context.Context, source, message string) {
	source = strings.TrimSpace(source)
	message = strings.TrimSpace(message)
	if source == "" {
		source = "unknown"
	}
	if message == "" {
		return
	}

	t.mu.Lock()
	hasSink := t.sink != nil
	threadID := t.id
	sessionKey := t.sessionKey
	t.mu.Unlock()
	if !hasSink {
		logger.Warn("wake on sinkless thread; dropping", "threadID", threadID, "sessionKey", sessionKey, "source", source)
		return
	}

	go func() {
		if _, err := t.Wake(WithSink(ctx), source, message); err != nil {
			logger.Warn("wake run failed", "threadID", threadID, "sessionKey", sessionKey, "source", source, "err", err)
		}
	}()
}

// WakeThread wakes a managed channel thread by session key.
// Kept for compatibility; defaults source to "external".
func (m *Manager) WakeThread(ctx context.Context, sessionKey, message string) error {
	return m.WakeThreadWithSource(ctx, sessionKey, "external", message)
}

// WakeThreadWithSource wakes a managed channel thread by session key and source.
func (m *Manager) WakeThreadWithSource(ctx context.Context, sessionKey, source, message string) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return fmt.Errorf("session key is required")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "external"
	}

	m.mu.Lock()
	t, ok := m.threads[sessionKey]
	m.mu.Unlock()
	if !ok || t == nil || t.Thread == nil {
		return fmt.Errorf("thread not found: %s", sessionKey)
	}

	t.WakeAsync(ctx, source, message)
	return nil
}

// WrapAgentTaskPlaceholder binds {{TASK}} in a prompt-builder agent.
func WrapAgentTaskPlaceholder(base *agent.Agent, task string) *agent.Agent {
	if base == nil {
		return nil
	}
	return &agent.Agent{
		Name:         base.Name,
		ProviderName: base.ProviderName,
		ModelType:    base.ModelType,
		BuildPrompt: func(ctx agent.PromptContext) string {
			if base.BuildPrompt == nil {
				return ""
			}
			prompt := base.BuildPrompt(ctx)
			return strings.ReplaceAll(prompt, "{{TASK}}", task)
		},
	}
}
