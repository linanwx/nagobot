package thread

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

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

// tryMerge drains the inbox for consecutive messages with the same
// Source + AgentName + Vars, concatenating their Message fields and
// keeping the last Sink.  Non-mergeable messages are re-enqueued.
func (t *Thread) tryMerge(first *WakeMessage) *WakeMessage {
	merged := 0
	var requeue []*WakeMessage
	for {
		select {
		case next := <-t.inbox:
			if canMerge(first, next) {
				first.Message += "\n" + next.Message
				first.Sink = next.Sink
				merged++
			} else {
				requeue = append(requeue, next)
			}
		default:
			for _, m := range requeue {
				t.inbox <- m
			}
			if merged > 0 {
				logger.Info("merged wake messages",
					"threadID", t.id,
					"sessionKey", t.sessionKey,
					"source", first.Source,
					"merged", merged+1,
					"requeued", len(requeue),
				)
			}
			return first
		}
	}
}

func canMerge(a, b *WakeMessage) bool {
	if a.Source != b.Source || a.AgentName != b.AgentName {
		return false
	}
	if len(a.Vars) != len(b.Vars) {
		return false
	}
	for k, v := range a.Vars {
		if b.Vars[k] != v {
			return false
		}
	}
	return true
}

// RunOnce dequeues one WakeMessage and executes a single turn.
func (t *Thread) RunOnce(ctx context.Context) {
	select {
	case msg := <-t.inbox:
		msg = t.tryMerge(msg)
		if name := strings.TrimSpace(msg.AgentName); name != "" {
			a, err := t.cfg().Agents.New(name)
			if err != nil {
				logger.Warn("agent not found, keeping current agent", "agent", name, "err", err)
			} else {
				t.mu.Lock()
				t.Agent = a
				t.mu.Unlock()
			}
		}
		for k, v := range msg.Vars {
			t.Set(k, v)
		}

		// Use per-wake sink; fall back to thread's default sink.
		sink := msg.Sink
		if sink.IsZero() {
			sink = t.defaultSink
		}

		// Resolve delivery label for the AI prompt.
		deliveryLabel := ""
		if !msg.Sink.IsZero() {
			deliveryLabel = msg.Sink.Label
		} else if !t.defaultSink.IsZero() {
			deliveryLabel = t.defaultSink.Label
		}

		userMessage := buildWakePayload(msg.Source, msg.Message, t.id, t.sessionKey, deliveryLabel)

		// Build injection function: between tool iterations, drain inbox for
		// mergeable user messages and inject them into the LLM conversation.
		injectFn := func() []provider.Message {
			var injected []provider.Message
			for {
				select {
				case next := <-t.inbox:
					if canMerge(msg, next) {
						payload := buildWakePayload(next.Source, next.Message, t.id, t.sessionKey, deliveryLabel)
						if payload != "" {
							injected = append(injected, provider.UserMessage(payload))
							logger.Info("injected mid-execution message",
								"threadID", t.id,
								"sessionKey", t.sessionKey,
								"source", next.Source,
							)
						}
					} else {
						t.inbox <- next // not mergeable, put back
						return injected
					}
				default:
					return injected
				}
			}
		}

		response, err := t.run(ctx, userMessage, sink, injectFn)
		if err != nil {
			logger.Error("thread run error", "threadID", t.id, "sessionKey", t.sessionKey, "source", msg.Source, "err", err)
			response = fmt.Sprintf("[Error] %v", err)
		}

		suppress := t.checkAndResetSuppressSink()
		if !sink.IsZero() && strings.TrimSpace(response) != "" && !suppress {
			if sinkErr := sink.Send(ctx, response); sinkErr != nil {
				logger.Error("sink delivery error", "threadID", t.id, "sessionKey", t.sessionKey, "err", sinkErr)
			}
		}
	default:
		// No message available; should not be called without pending messages.
	}
}

// buildWakePayload constructs the user message from a wake source and message.
func buildWakePayload(source, message, threadID, sessionKey, deliveryLabel string) string {
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
		"[Wake reason: %s | thread: %s | session: %s | %s (%s, %s, UTC%s)]",
		source,
		threadID,
		sessionKey,
		now.Format(time.RFC3339),
		now.Weekday().String(),
		now.Location().String(),
		now.Format("-07:00"),
	)

	var deliveryHint string
	if deliveryLabel != "" {
		deliveryHint = fmt.Sprintf("[Delivery: %s]", deliveryLabel)
	} else {
		deliveryHint = "[Delivery: no auto-delivery, use tools to send messages if needed]"
	}

	action := wakeActionHint(source)
	if action == "" {
		return wakeHeader + "\n" + deliveryHint + "\n" + message
	}
	return wakeHeader + "\n" + deliveryHint + "\n[Wake Action]\n" + action + "\n\n" + message
}

func wakeActionHint(source string) string {
	switch source {
	case "telegram", "cli", "web", "discord":
		return "Respond directly to the user request."
	case "user_active":
		return "Resume the target session and respond to this wake message."
	case "child_task":
		return "Execute this delegated task and return a result."
	case "child_completed":
		return "A child thread completed. Summarize the result and report the original result."
	case "sleep_completed":
		return "You previously set a sleep timer. You have been woken up. Resume your session."
	case "cron":
		return "A scheduled cron task has started. Execute it based on the provided job context."
	case "cron_finished":
		return "A cron task has finished. Summarize the result and report the original result."
	case "external":
		return "Process this external wake message and continue the session."
	default:
		return "Process this wake message and continue."
	}
}
