package thread

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	sysmsg "github.com/linanwx/nagobot/thread/msg"
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

		loc := t.location()
		userMessage := buildWakePayload(msg.Source, msg.Message, t.id, t.sessionKey, deliveryLabel, loc)

		// Build injection function: between tool iterations, drain inbox for
		// mergeable user messages and inject them into the LLM conversation.
		injectFn := func() []provider.Message {
			var injected []provider.Message
			for {
				select {
				case next := <-t.inbox:
					if canMerge(msg, next) {
						payload := buildWakePayload(next.Source, next.Message, t.id, t.sessionKey, deliveryLabel, loc)
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
			response = sysmsg.BuildSystemMessage("error", nil, fmt.Sprintf("%v", err))
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
// Uses XML format with per-source visibility annotations so the AI knows
// which content the user can see and which is assistant-only.
func buildWakePayload(source WakeSource, message, threadID, sessionKey, deliveryLabel string, loc *time.Location) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	if source == "" {
		source = "unknown"
	}

	now := time.Now().In(loc)

	var delivery string
	if deliveryLabel != "" {
		delivery = deliveryLabel
	} else {
		delivery = "no auto-delivery, use tools to send messages if needed"
	}

	var sb strings.Builder
	sb.WriteString("<wake>\n")
	fmt.Fprintf(&sb, "  <source>%s</source>\n", source)
	fmt.Fprintf(&sb, "  <thread>%s</thread>\n", threadID)
	fmt.Fprintf(&sb, "  <session>%s</session>\n", sessionKey)
	fmt.Fprintf(&sb, "  <time>%s (%s, %s, UTC%s)</time>\n",
		now.Format(time.RFC3339),
		now.Weekday().String(),
		now.Location().String(),
		now.Format("-07:00"),
	)
	fmt.Fprintf(&sb, "  <delivery>%s</delivery>\n", delivery)
	fmt.Fprintf(&sb, "  <action>%s</action>\n", wakeActionHint(source))
	fmt.Fprintf(&sb, "  <message visibility=%q>\n%s\n  </message>\n", messageVisibility(source), message)
	sb.WriteString("</wake>")

	return sb.String()
}

// messageVisibility returns the visibility label for a wake source.
// User-originated messages are "user-visible"; system messages are "assistant-only".
func messageVisibility(source WakeSource) string {
	switch source {
	case WakeTelegram, WakeCLI, WakeWeb, WakeDiscord, WakeFeishu:
		return "user-visible"
	default:
		return "assistant-only"
	}
}

func wakeActionHint(source WakeSource) string {
	switch source {
	case WakeTelegram, WakeCLI, WakeWeb, WakeDiscord, WakeFeishu:
		return "A user sent a message. React accordingly."
	case WakeUserActive:
		return "Resume the target session and respond to this wake message. The <message> content is only visible to you."
	case WakeChildTask:
		return "A parent thread delegated a task to you. Execute this task and output the result."
	case WakeChildCompleted:
		return "A child thread completed. The <message> content is ONLY visible to you. The user cannot see it. Include the complete result in your response."
	case WakeSleepCompleted:
		return "Your sleep timer expired. The <message> is system context only. Resume your session."
	case WakeCron:
		return "A scheduled cron task has started. Execute it based on the provided job context."
	case WakeCronFinished:
		return "A cron task finished. The <message> content is ONLY visible to you. Summarize and deliver the result to the user."
	case WakeExternal:
		return "Process this external wake message. The <message> content is only visible to you."
	default:
		return "Process this wake message and continue."
	}
}
