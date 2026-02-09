package thread

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// WakeMessage is an item in a thread's wake queue.
type WakeMessage struct {
	Source    string            // Wake source: "user_message", "cron", "child_completed", etc.
	Message   string            // Wake payload text.
	Sink      Sink              // Per-wake response delivery (nil = drop response).
	SinkLabel string            // Descriptive label for this sink.
	AgentName string            // Optional agent name override for this wake.
	Vars      map[string]string // Optional vars override for this wake.
}

// Enqueue adds a wake message to the thread's inbox and notifies the manager.
func (t *Thread) Enqueue(msg *WakeMessage) {
	if msg == nil {
		return
	}
	t.inbox <- msg
	// Non-blocking notify: if signal already has a pending notification, skip.
	select {
	case t.signal <- struct{}{}:
	default:
	}
}

// hasMessages returns true if the thread's inbox has pending messages.
func (t *Thread) hasMessages() bool {
	return len(t.inbox) > 0
}

// RunOnce dequeues one WakeMessage and executes a single turn.
func (t *Thread) RunOnce(ctx context.Context) {
	select {
	case msg := <-t.inbox:
		if name := strings.TrimSpace(msg.AgentName); name != "" {
			t.mu.Lock()
			t.Agent = t.cfg().Agents.New(name)
			t.mu.Unlock()
		}
		for k, v := range msg.Vars {
			t.Set(k, v)
		}

		userMessage := buildWakePayload(msg.Source, msg.Message)
		response, err := t.run(ctx, userMessage)
		if err != nil {
			logger.Error("thread run error", "threadID", t.id, "sessionKey", t.sessionKey, "source", msg.Source, "err", err)
		}

		if msg.Sink != nil && strings.TrimSpace(response) != "" {
			if sinkErr := msg.Sink(ctx, response); sinkErr != nil {
				logger.Error("sink delivery error", "threadID", t.id, "sessionKey", t.sessionKey, "err", sinkErr)
			}
		}
	default:
		// No message available; should not be called without pending messages.
	}
}

// buildWakePayload constructs the user message from a wake source and message.
func buildWakePayload(source, message string) string {
	source = strings.TrimSpace(source)
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
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
		return wakeHeader + "\n" + message
	}
	return wakeHeader + "\n[Wake Action]\n" + action + "\n\n" + message
}

func wakeActionHint(source string) string {
	switch source {
	case "user_message":
		return "Respond directly to the user request."
	case "user_active":
		return "Resume the target session and respond to this wake message."
	case "child_task":
		return "Execute this delegated task and return a result."
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
