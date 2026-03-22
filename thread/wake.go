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

// hasMessages returns true if the thread's inbox has pending messages
// or there are deferred messages from a previous tryMerge.
func (t *Thread) hasMessages() bool {
	return len(t.pending) > 0 || len(t.inbox) > 0
}

// tryMerge drains the inbox for consecutive messages with the same
// Source + AgentName + Vars, concatenating their Message fields and
// keeping the last Sink.  Non-mergeable messages are stored in t.pending
// (instead of requeuing to the channel) to avoid deadlock when the inbox
// buffer is full.
func (t *Thread) tryMerge(first *WakeMessage) *WakeMessage {
	merged := 0
	var deferred []*WakeMessage
	for {
		select {
		case next := <-t.inbox:
			if canMerge(first, next) {
				first.Message += "\n" + next.Message
				first.Sink = next.Sink
				merged++
			} else {
				deferred = append(deferred, next)
			}
		default:
			// Store non-mergeable messages for the next RunOnce call
			// rather than pushing them back into the channel.
			t.pending = append(t.pending, deferred...)
			if merged > 0 {
				logger.Info("merged wake messages",
					"threadID", t.id,
					"sessionKey", t.sessionKey,
					"source", first.Source,
					"merged", merged+1,
					"deferred", len(deferred),
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
	// Don't merge messages with different Sinks to prevent cross-delivery
	// (e.g. cron results leaking to a user's channel sink).
	if a.Sink.Label != b.Sink.Label {
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

// dequeue returns the next WakeMessage, preferring deferred messages
// (from a previous tryMerge) over the inbox channel.
func (t *Thread) dequeue() (*WakeMessage, bool) {
	if len(t.pending) > 0 {
		m := t.pending[0]
		t.pending = t.pending[1:]
		return m, true
	}
	select {
	case m := <-t.inbox:
		return m, true
	default:
		return nil, false
	}
}

// RunOnce dequeues one WakeMessage and executes a single turn.
func (t *Thread) RunOnce(ctx context.Context) {
	msg, ok := t.dequeue()
	if !ok {
		return
	}
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
	} else if fn := t.cfg().DefaultAgentFor; fn != nil {
		// No explicit agent override in the wake message — check if the default
		// agent for this session has changed (hot-reload from set-agent).
		if newAgent := fn(t.sessionKey); newAgent != "" {
			t.mu.Lock()
			currentName := ""
			if t.Agent != nil {
				currentName = t.Agent.Name
			}
			t.mu.Unlock()
			if newAgent != currentName {
				if a, err := t.cfg().Agents.New(newAgent); err == nil {
					t.mu.Lock()
					t.Agent = a
					t.mu.Unlock()
					logger.Info("agent hot-reloaded", "sessionKey", t.sessionKey, "from", currentName, "to", newAgent)
				}
			}
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
	// Resolve agent name for the wake payload.
	agentName := ""
	t.mu.Lock()
	if t.Agent != nil {
		agentName = t.Agent.Name
	}
	t.mu.Unlock()
	userMessage := buildWakePayload(msg.Source, msg.Message, t.id, t.sessionKey, sessionDir, deliveryLabel, modelLabel, agentName, loc)

	// Build injection function: between tool iterations, drain inbox for
	// mergeable user messages and inject them into the LLM conversation.
	// Non-mergeable messages are stored in t.pending to avoid channel
	// requeue deadlock.
	injectFn := func() []provider.Message {
		var injected []provider.Message
		for {
			select {
			case next := <-t.inbox:
				if canMerge(msg, next) {
					payload := buildWakePayload(next.Source, next.Message, t.id, t.sessionKey, sessionDir, deliveryLabel, modelLabel, agentName, loc)
					if payload != "" {
						injected = append(injected, provider.UserMessage(payload))
						logger.Info("injected mid-execution message",
							"threadID", t.id,
							"sessionKey", t.sessionKey,
							"source", next.Source,
						)
					}
				} else {
					t.pending = append(t.pending, next) // not mergeable, defer
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
}

// buildWakePayload constructs the user message from a wake source and message.
// Uses YAML frontmatter + markdown body so the AI knows the wake context
// and which content the user can see vs assistant-only.
func buildWakePayload(source WakeSource, message, threadID, sessionKey, sessionDir, deliveryLabel, model, agent string, loc *time.Location) string {
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
		Agent:      agent,
		Delivery:   delivery,
		Visibility: messageVisibility(source),
	}
	if hint := wakeActionHint(source); hint != "" {
		header.Action = hint
	}
	// For user-visible sources (telegram, discord, etc.) that may contain media,
	// include the current model's multimodal capabilities so the LLM knows what it can do.
	if sysmsg.IsUserVisibleSource(source) && model != "" {
		if prov, mod, ok := strings.Cut(model, "/"); ok {
			v := provider.SupportsVision(prov, mod)
			a := provider.SupportsAudio(prov, mod)
			header.SupportsVision = &v
			header.SupportsAudio = &a
		}
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
	Source         string `yaml:"source"`
	Thread         string `yaml:"thread"`
	Session        string `yaml:"session"`
	SessionDir     string `yaml:"session_dir,omitempty"`
	Time           string `yaml:"time"`
	Model          string `yaml:"model,omitempty"`
	Agent          string `yaml:"agent,omitempty"`
	Delivery       string `yaml:"delivery"`
	Visibility     string `yaml:"visibility"`
	Action         string `yaml:"action,omitempty"`
	SupportsVision *bool  `yaml:"supports_vision,omitempty"`
	SupportsAudio  *bool  `yaml:"supports_audio,omitempty"`
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
		return "A user sent a message. React accordingly and be friendly."
	}
	switch source {
	case WakeUserActive:
		return "Resume the target session and respond to this wake message. The content is only visible to you."
	case WakeChildTask:
		return "A parent thread delegated a task to you. Execute this task and output the result."
	case WakeChildCompleted:
		return "A child thread completed. The content is ONLY visible to you. The user cannot see it. React accordingly and be friendly."
	case WakeSleepCompleted:
		return "Your sleep timer expired. The message is system context only. Resume your session."
	case WakeCron:
		return "A scheduled cron task has started. Execute it based on the provided job context."
	case WakeCronFinished:
		return "A cron task finished. The content is ONLY visible to you. React accordingly and be friendly."
	case WakeExternal:
		return "Process this external wake message. The content is only visible to you."
	case WakeCompression:
		return "Automated background maintenance. Execute the compression skill immediately. Do not produce user-facing content."
	case WakeHeartbeat:
		return "Heartbeat pulse. Load the heartbeat-wake skill and follow its instructions."
	case WakeResume:
		return "The system restarted while your previous turn was in progress. The original request is included below. Continue processing where you left off. If you believe the request is no longer relevant, call sleep_thread to skip."
	default:
		return "Process this wake message and continue."
	}
}
