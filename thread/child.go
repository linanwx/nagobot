package thread

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// SpawnChild spawns a child thread for delegated work. Always asynchronous:
// returns child ID immediately, and the child wakes the parent via
// "child_completed" when done.
func (t *Thread) SpawnChild(ctx context.Context, agentName string, task string) (string, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	if t.mgr == nil {
		return "", fmt.Errorf("thread has no manager, cannot spawn child")
	}

	childSessionKey := t.generateChildSessionKey()
	child, err := t.mgr.NewThread(childSessionKey, agentName)
	if err != nil {
		return "", fmt.Errorf("spawn child: %w", err)
	}
	child.Set("TASK", task)

	parentThread := t
	child.Enqueue(&WakeMessage{
		Source:  "child_task",
		Message: task,
		Sink: func(_ context.Context, response string) error {
			var message string
			if strings.TrimSpace(response) != "" {
				message = fmt.Sprintf("Child %s completed:\n%s", child.id, response)
			} else {
				message = fmt.Sprintf("Child %s completed (no output)", child.id)
			}
			parentThread.Enqueue(&WakeMessage{
				Source:  "child_completed",
				Message: message,
			})
			return nil
		},
	})

	logger.Debug("child thread spawned", "parentID", t.id, "childID", child.id)
	return child.id, nil
}


func (t *Thread) generateChildSessionKey() string {
	if t.cfg().Sessions == nil {
		return ""
	}
	parent := strings.TrimSpace(t.sessionKey)
	if parent == "" {
		parent = strings.TrimSpace(t.id)
	}
	if parent == "" {
		parent = "main"
	}

	timePart := time.Now().Local().Format("2006-01-02-15-04-05")
	suffix := RandomHex(4)
	if suffix == "" {
		suffix = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%s:threads:%s-%s", parent, timePart, suffix)
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
