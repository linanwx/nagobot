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
		return child.Run(ctx, userMessage)
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
		result, err := child.Run(ctx, userMessage)
		t.mu.Lock()
		state.result = result
		state.err = err
		close(state.done)
		t.pendingResults = append(t.pendingResults, pendingChildResult{ID: childID, Result: result, Err: err})
		shouldStartAuto := t.sink != nil && !t.autoRunning
		if shouldStartAuto {
			t.autoRunning = true
		}
		t.mu.Unlock()

		if shouldStartAuto {
			go t.runAsyncContinuations(ctx)
		}
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

// Wake appends a wake message and triggers auto continuation when sink exists.
func (t *Thread) Wake(ctx context.Context, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}

	t.mu.Lock()
	t.pendingResults = append(t.pendingResults, pendingChildResult{
		ID:     "wake",
		Result: message,
	})
	shouldStartAuto := t.sink != nil && !t.autoRunning
	if shouldStartAuto {
		t.autoRunning = true
	}
	t.mu.Unlock()

	if shouldStartAuto {
		go t.runAsyncContinuations(ctx)
	}
}

// WakeThread wakes a managed channel thread by session key.
func (m *Manager) WakeThread(ctx context.Context, sessionKey, message string) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return fmt.Errorf("session key is required")
	}

	m.mu.Lock()
	t, ok := m.threads[sessionKey]
	m.mu.Unlock()
	if !ok || t == nil || t.Thread == nil {
		return fmt.Errorf("thread not found: %s", sessionKey)
	}

	t.Wake(ctx, message)
	return nil
}

func (t *Thread) drainPendingResults() string {
	t.mu.Lock()
	pending := t.pendingResults
	t.pendingResults = nil
	t.mu.Unlock()

	if len(pending) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Async thread results since last turn]\n")
	for _, r := range pending {
		if r.Err != nil {
			sb.WriteString(fmt.Sprintf("- %s failed: %v\n", r.ID, r.Err))
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s completed: %s\n", r.ID, r.Result))
	}
	return sb.String()
}

func (t *Thread) runAsyncContinuations(ctx context.Context) {
	defer func() {
		t.mu.Lock()
		t.autoRunning = false
		shouldRestart := t.sink != nil && len(t.pendingResults) > 0
		if shouldRestart {
			t.autoRunning = true
		}
		t.mu.Unlock()

		if shouldRestart {
			go t.runAsyncContinuations(ctx)
		}
	}()

	for {
		t.mu.Lock()
		pendingCount := len(t.pendingResults)
		hasSink := t.sink != nil
		t.mu.Unlock()

		if pendingCount == 0 || !hasSink {
			return
		}

		if _, err := t.Run(WithSink(ctx), ""); err != nil {
			logger.Warn("async child continuation failed", "threadID", t.id, "err", err)
			return
		}
	}
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
