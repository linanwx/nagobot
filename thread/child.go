package thread

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/thread/msg"
	"github.com/linanwx/nagobot/tools"
)

// SpawnChild spawns a child thread for delegated work. Always asynchronous:
// returns child ID immediately, and the child wakes the parent via
// "child_completed" when done.
func (t *Thread) SpawnChild(ctx context.Context, agentName string, task string) (*tools.SpawnResult, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return nil, fmt.Errorf("task is required")
	}
	if t.mgr == nil {
		return nil, fmt.Errorf("thread has no manager, cannot spawn child")
	}

	childSessionKey := t.generateChildSessionKey()
	child, err := t.mgr.NewThread(childSessionKey, agentName)
	if err != nil {
		return nil, fmt.Errorf("spawn child: %w", err)
	}
	child.Set("TASK", task)
	child.parent = t

	// Sink-to-parent: shared by per-wake sink and defaultSink.
	parentThread := t
	sinkToParent := Sink{
		Label: "your response will be forwarded to parent thread",
		Send: func(_ context.Context, response string) error {
			fields := child.completionFields()
			content := strings.TrimSpace(response)
			if content == "" {
				content = "no output"
			}
			message := msg.BuildSystemMessage("child_completed", fields, content)
			parentThread.Enqueue(&WakeMessage{
				Source:  WakeChildCompleted,
				Message: message,
			})
			return nil
		},
	}
	child.defaultSink = sinkToParent

	child.Enqueue(&WakeMessage{
		Source:  WakeChildTask,
		Message: task,
		Sink:    sinkToParent,
	})

	logger.Debug("child thread spawned", "parentID", t.id, "childID", child.id)

	result := &tools.SpawnResult{ID: child.id}
	if child.Agent != nil {
		result.Agent = child.Agent.Name
	}
	result.Provider, result.Model = child.resolvedProviderModel()
	cfg := child.cfg()
	if cfg.Agents != nil && child.Agent != nil {
		if def := cfg.Agents.Def(child.Agent.Name); def != nil {
			result.Specialty = def.Specialty
		}
	}
	return result, nil
}


// completionFields returns metadata fields for the child_completed message.
func (t *Thread) completionFields() map[string]string {
	fields := map[string]string{"child_id": t.id}
	if t.Agent != nil {
		fields["agent"] = t.Agent.Name
	}
	providerName, modelName := t.resolvedProviderModel()
	if providerName != "" {
		fields["provider"] = providerName
	}
	if modelName != "" {
		fields["model"] = modelName
	}
	cfg := t.cfg()
	if cfg.Agents != nil && t.Agent != nil {
		if def := cfg.Agents.Def(t.Agent.Name); def != nil && def.Specialty != "" {
			fields["specialty"] = def.Specialty
		}
	}
	return fields
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
		parent = "cli"
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
