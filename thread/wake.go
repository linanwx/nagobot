package thread

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	sysmsg "github.com/linanwx/nagobot/thread/msg"
	"gopkg.in/yaml.v3"
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
		t.lastWakeSource = msg.Source
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

		// Heartbeat reflection is always silent — suppress sink delivery.
		if msg.Source == WakeHeartbeatReflect {
			t.SetSuppressSink()
		}

		// Use per-wake sink; fall back to thread's default sink.
		sink := msg.Sink
		if sink.IsZero() {
			sink = t.defaultSink
		}
		// System-initiated wakes: suppress intermediate delivery (streaming +
		// OnMessage) while keeping final response delivery via OnFinalResponse.
		if messageVisibility(msg.Source) == "assistant-only" {
			sink = sink.WithoutStreaming()
		}
		// Resolve delivery label for the AI prompt.
		deliveryLabel := ""
		if !msg.Sink.IsZero() {
			deliveryLabel = msg.Sink.Label
		} else if !t.defaultSink.IsZero() {
			deliveryLabel = t.defaultSink.Label
		}

		loc := t.location()
		prov, mod := t.resolvedProviderModel()
		modelLabel := prov + "/" + mod
		sessionDir := t.mgr.SessionDir(t.sessionKey)
		userMessage := buildWakePayload(msg.Source, msg.Message, t.id, t.sessionKey, sessionDir, deliveryLabel, modelLabel, loc)

		// Build injection function: between tool iterations, drain inbox for
		// mergeable user messages and inject them into the LLM conversation.
		injectFn := func() []provider.Message {
			var injected []provider.Message
			for {
				select {
				case next := <-t.inbox:
					if canMerge(msg, next) {
						payload := buildWakePayload(next.Source, next.Message, t.id, t.sessionKey, sessionDir, deliveryLabel, modelLabel, loc)
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

		response, err := t.run(ctx, userMessage, sink, injectFn, string(msg.Source))
		t.checkAndResetSuppressSink()

		if err != nil {
			logger.Error("thread run error", "threadID", t.id, "sessionKey", t.sessionKey, "source", msg.Source, "err", err)
			errMsg := sysmsg.BuildSystemMessage("error", nil, fmt.Sprintf("%v", err))
			if !sink.IsZero() {
				if sinkErr := sink.WithRetry(3).Send(ctx, errMsg); sinkErr != nil {
					logger.Error("sink delivery error", "threadID", t.id, "sessionKey", t.sessionKey, "err", sinkErr)
				}
			}
		}
		_ = response // persisted inside run(); not delivered here
	default:
		// No message available; should not be called without pending messages.
	}
}

// buildWakePayload constructs the user message from a wake source and message.
// Uses YAML frontmatter + markdown body so the AI knows the wake context
// and which content the user can see vs assistant-only.
func buildWakePayload(source WakeSource, message, threadID, sessionKey, sessionDir, deliveryLabel, model string, loc *time.Location) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	if source == "" {
		source = "unknown"
	}

	now := time.Now().In(loc)

	delivery := deliveryLabel
	if delivery == "" {
		delivery = "no auto-delivery, use tools to send messages if needed"
	}

	header := wakeHeader{
		Source:     string(source),
		Thread:     threadID,
		Session:    sessionKey,
		SessionDir: sessionDir,
		Time:       fmt.Sprintf("%s (%s, %s, UTC%s)", now.Format(time.RFC3339), now.Weekday(), now.Location(), now.Format("-07:00")),
		Model:      model,
		Delivery:   delivery,
		Visibility: messageVisibility(source),
	}
	if hint := wakeActionHint(source); hint != "" {
		header.Action = hint
	}

	yamlBytes, _ := yaml.Marshal(header)

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(yamlBytes)
	sb.WriteString("---\n\n")
	sb.WriteString(message)

	return sb.String()
}

// wakeHeader is the YAML frontmatter for wake messages.
type wakeHeader struct {
	Source     string `yaml:"source"`
	Thread     string `yaml:"thread"`
	Session    string `yaml:"session"`
	SessionDir string `yaml:"session_dir,omitempty"`
	Time       string `yaml:"time"`
	Model      string `yaml:"model,omitempty"`
	Delivery   string `yaml:"delivery"`
	Visibility string `yaml:"visibility"`
	Action     string `yaml:"action,omitempty"`
}

// messageVisibility returns the visibility label for a wake source.
// User-originated messages are "user-visible"; system messages are "assistant-only".
func messageVisibility(source WakeSource) string {
	if sysmsg.IsUserVisibleSource(source) {
		return "user-visible"
	}
	return "assistant-only"
}

func wakeActionHint(source WakeSource) string {
	if sysmsg.IsUserVisibleSource(source) {
		return "A user sent a message. React accordingly."
	}
	switch source {
	case WakeUserActive:
		return "Resume the target session and respond to this wake message. The content is only visible to you."
	case WakeChildTask:
		return "A parent thread delegated a task to you. Execute this task and output the result."
	case WakeChildCompleted:
		return "A child thread completed. The content is ONLY visible to you. The user cannot see it. Include the complete result in your response."
	case WakeSleepCompleted:
		return "Your sleep timer expired. The message is system context only. Resume your session."
	case WakeCron:
		return "A scheduled cron task has started. Execute it based on the provided job context."
	case WakeCronFinished:
		return "A cron task finished. The content is ONLY visible to you. Summarize and deliver the result to the user."
	case WakeExternal:
		return "Process this external wake message. The content is only visible to you."
	case WakeCompression:
		return "Automated background maintenance. Execute the compression skill immediately. Do not produce user-facing content."
	case WakeHeartbeatReflect:
		return "Heartbeat reflection triggered. Load the specified skill and follow its instructions to review this session and update heartbeat.md."
	case WakeHeartbeatWake:
		return "Heartbeat wake triggered. Load the specified skill and follow its instructions to check heartbeat.md and act on relevant items."
	default:
		return "Process this wake message and continue."
	}
}
