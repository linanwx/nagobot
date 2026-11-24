package thread

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/obs"
	"github.com/linanwx/nagobot/provider"
	sysmsg "github.com/linanwx/nagobot/thread/msg"
)

// queueWaitMs is how long a wake sat between enqueue and the turn starting.
// Returns 0 for a wake with no enqueue stamp rather than a nonsense age.
func queueWaitMs(enqueuedAt time.Time) int64 {
	if enqueuedAt.IsZero() {
		return 0
	}
	return time.Since(enqueuedAt).Milliseconds()
}

// Enqueue adds a wake message to the thread's inbox and notifies the manager.
func (t *Thread) Enqueue(msg *WakeMessage) {
	if msg == nil {
		return
	}
	if msg.EnqueuedAt.IsZero() {
		msg.EnqueuedAt = time.Now()
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

// EnqueueInject pushes a control/injection message onto the dedicated inject
// queue. Unlike Enqueue, these bypass tryMerge/canMerge entirely and are drained
// unconditionally by the running turn's injectFn at each iteration boundary.
// Non-blocking: returns an error if the inject queue is full rather than
// blocking the caller (typically an RPC handler).
func (t *Thread) EnqueueInject(body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("inject body is empty")
	}
	select {
	case t.injectInbox <- body:
		// Notify the manager in case the thread is idle-but-loaded, so the
		// queue is not left unattended. Harmless for a running thread.
		select {
		case t.signal <- struct{}{}:
		default:
		}
		return nil
	default:
		return fmt.Errorf("inject queue full for session %q", t.sessionKey)
	}
}

// tryMerge drains the inbox for consecutive messages with the same
// Source + AgentName + Vars, concatenating their Message fields and
// keeping the last message's sinks.  Non-mergeable messages are stored in t.pending
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
				first.Sinks = next.Sinks
				first.CallerSink = next.CallerSink
				// The reaction targets ONE inbound message; the merged turn is
				// answered as a whole, so it reacts on the last one merged.
				first.MessageSink = next.MessageSink
				// The merged turn renders ONE frontmatter header, so media
				// fields from later messages must fold in or they vanish.
				first.MediaInfo = joinNonEmpty(first.MediaInfo, next.MediaInfo)
				first.MediaPreview = joinNonEmpty(first.MediaPreview, next.MediaPreview)
				// A merged turn from different speakers has no single sender —
				// blank the id rather than mis-attribute the whole turn.
				if first.SenderID != next.SenderID {
					first.SenderID = ""
				}
				// Message ids do NOT fold: N wakes become one persisted
				// message, so only the first id can name it. The sender of a
				// later one never sees its id on disk — its text is inside the
				// first one's message, which is also how the web client retires
				// its queued chips (text containment, not id equality).
				//
				// Traces do not fold either, for the same reason and with the
				// opposite remedy: each merged message keeps its own trace, and
				// the turn links to all of them. Overwriting Traceparent the way
				// the sinks above are overwritten would orphan every message but
				// the last.
				if next.Traceparent != "" {
					first.MergedTraceparents = append(first.MergedTraceparents, next.Traceparent)
				}
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
	if a.AgentName != b.AgentName {
		return false
	}
	// Don't merge wakes with different model overrides — the merged body would
	// otherwise run under the first wake's model, silently mis-routing the second.
	if a.OverrideProvider != b.OverrideProvider || a.OverrideModel != b.OverrideModel {
		return false
	}
	return turnContextMismatch(a, b) == ""
}

// canInject decides whether a wake that arrives while a turn is running joins
// that turn at the next iteration boundary.
func canInject(running, next *WakeMessage, runningAgent string, defaultAgent func() string) bool {
	return injectRejectReason(running, next, runningAgent, defaultAgent) == ""
}

// injectRejectReason is canInject with the reason spelled out, so a deferred
// wake can be logged with it. It returns "" when the wake may be injected.
//
// It is canMerge with two differences, and both exist for the same reason: a
// dispatch follow-up to a running child (same task_id) normally carries only a
// body. It omits `agent`, and it omits `provider`/`model`, because those were
// given once when the child was spawned. An omitted field means "keep what is
// running", never "use the default".
//
//   - Agents are compared by the agent the wake would actually RUN under. An
//     empty AgentName means "keep this session's agent" (RunOnce resolves it
//     from meta.json via DefaultAgentFor, and keeps the current agent when that
//     yields nothing).
//   - A model override is compared only when the follow-up names one. An
//     injected message is answered inside the running turn, on the running
//     turn's model, so an empty override has nothing to disagree with. The
//     strict comparison deferred every steer sent to a child spawned with an
//     explicit model: on kingsley (2026-11-24) six follow-ups, including "stop
//     editing the file now", reached a coder child only after its 15-minute,
//     48-iteration turn had finished the work they were trying to stop.
//
// tryMerge keeps the strict canMerge: at turn start nothing is running yet, so
// an empty field has no running value to inherit and two such wakes really are
// two different turns.
func injectRejectReason(running, next *WakeMessage, runningAgent string, defaultAgent func() string) string {
	nextAgent := strings.TrimSpace(next.AgentName)
	if nextAgent == "" && defaultAgent != nil {
		nextAgent = defaultAgent()
	}
	if nextAgent != "" && nextAgent != runningAgent {
		return "agent " + nextAgent + " != running " + runningAgent
	}
	if next.OverrideProvider != "" || next.OverrideModel != "" {
		if next.OverrideProvider != running.OverrideProvider || next.OverrideModel != running.OverrideModel {
			return "model override " + next.OverrideProvider + "/" + next.OverrideModel +
				" != running " + running.OverrideProvider + "/" + running.OverrideModel
		}
	}
	return turnContextMismatch(running, next)
}

// turnContextMismatch is everything canMerge and canInject agree on: two wakes
// can share one turn only if they come from the same source, deliver to the
// same place, answer the same caller and carry the same vars. It returns the
// first mismatch, or "" when there is none.
func turnContextMismatch(a, b *WakeMessage) string {
	if a.Source != b.Source {
		return "source " + string(b.Source) + " != " + string(a.Source)
	}
	// Don't merge messages with different sinks to prevent cross-delivery
	// (e.g. cron results leaking to a user's channel sink).
	if a.Sinks.Label() != b.Sinks.Label() {
		return "sinks differ"
	}
	// Two peers asking us things are two conversations, and a merged turn keeps
	// one caller. The paired sink's label used to carry this distinction into the
	// comparison above; with the caller sink split out of Sinks it has to be
	// stated, or wakes from different sessions would fold into one turn that can
	// only answer one of them.
	if a.CallerSessionKey != b.CallerSessionKey {
		return "caller " + b.CallerSessionKey + " != " + a.CallerSessionKey
	}
	if len(a.Vars) != len(b.Vars) {
		return "vars differ"
	}
	for k, v := range a.Vars {
		if b.Vars[k] != v {
			return "vars differ"
		}
	}
	return ""
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

// Two very different turns used to share one `delivery` string, and the advice
// half of it was a trap on the one that fires most.
//
// silentDeliveryLabel covers heartbeat and compression, where resolveTurnSinks
// empties the set unconditionally. It states the destination and stops. The old
// text ended with "use dispatch(to=session, ...) if something must be sent",
// which on such a turn points at a wall: the only session whose human this turn
// might want is its OWN, and dispatch rejects a self-reference in both the
// session_key and the channel+user_id form. So it pushed the model toward a call
// that cannot succeed, on the single largest consumer of prompt tokens in the
// system. Reaching a human later is a scheduled self-wake (manage-cron's
// `set-at --direct-wake`), which is where dream already documents it — not
// something worth carrying in a field every one of these turns reads.
//
// noDeliveryLabel covers the other case: a turn that is NOT silent, on a session
// with no channel of its own. There the advice is real and must stay, because
// plain text vanishes there and dispatch is the only way out. It names both
// forms since the right one depends on the caller: to=caller:session when a peer
// session woke us, to=session when nothing did (a resume, say) and
// to=caller:session would be rejected for having no session caller.
const silentDeliveryLabel = "nothing you write this turn reaches anyone — this is an internal maintenance turn"

const noDeliveryLabel = "this session has no channel of its own — nothing you write this turn reaches anyone; " +
	"route anything that must be sent with dispatch(to=caller:session), " +
	"or dispatch(to=session, ...) when there is no caller"

// fallbackDeliveryLabel picks between the two above for a turn whose sinks
// resolved to nothing.
func fallbackDeliveryLabel(source WakeSource) string {
	if isSilentSource(source) {
		return silentDeliveryLabel
	}
	return noDeliveryLabel
}

// resolveDeliveryLabel renders the wake payload's `delivery` field: a
// natural-language statement of where this turn's plain content goes.
//
// It reads that destination from the SAME function that routes the content
// (contentSink) — never from the raw wake sink. The two diverge on every
// proactive source: a peer-session / cron / progress wake on a user-facing
// session carries a sink pointing back at whoever woke us, while contentSink
// sends plain content to the channel user instead. Naming the wake sink there
// told the turn its output went to the caller, which is false, and contradicted
// two things at once — the wake's own action hint ("Plain text goes to your own
// human instead") and the wake-and-delivery section, which names `delivery` as
// the one place a turn learns where its output goes.
// source is a parameter rather than a read of t.lastWakeSource because
// contentSink takes t.mu itself and Go mutexes are not reentrant — holding the
// lock across this call would deadlock the turn.
func (t *Thread) resolveDeliveryLabel(source WakeSource, wakeSink SinkSet) string {
	out, _ := t.contentSink(wakeSink)
	if out.IsZero() || out.Label() == "" {
		return fallbackDeliveryLabel(source)
	}
	return out.Label()
}

// resolveTurnSinks decides where a turn's output goes: the channel this wake
// arrived on, unioned OVER the session's own standing destinations.
//
// Union, not replacement — a session's output always reaches the channel it
// lives on, whoever prompted the turn. A WeCom message aimed at a Discord
// session therefore answers on WeCom, in the Discord channel, and on any web
// page watching it; a Discord message aimed at the same session does not answer
// on Discord twice, because Union deduplicates by SessionSink.Channel and the
// inbound sink wins (it carries the turn's chat.jsonl buffer).
//
// A silent source gets the empty set and keeps it. That is why the old "an empty
// wake sink falls back to the default set" rule is gone: an empty set now means
// something, and filling it back in would hand a heartbeat turn the user's
// channel.
func resolveTurnSinks(source WakeSource, wakeSinks, defaultSinks SinkSet) SinkSet {
	if isSilentSource(source) {
		return SinkSet{}
	}
	out := wakeSinks.Union(defaultSinks)
	// System-initiated wakes: no chunked delivery, so only the final message
	// reaches a channel. Rich streaming is deliberately left alone — a client
	// watching this session still follows a system-initiated turn live.
	if messageSender(source) == "system" {
		out = out.WithoutChunking()
	}
	return out
}

// RunOnce dequeues one WakeMessage and executes a single turn.
func (t *Thread) RunOnce(ctx context.Context) {
	msg, ok := t.dequeue()
	if !ok {
		return
	}
	msg = t.tryMerge(msg)

	// Rejoin the trace the producer started. The ctx handed to RunOnce is the
	// manager's process-lifetime ctx, so without this every turn would open a
	// fresh root and the queue wait would be invisible.
	ctx = obs.ContextWith(ctx, msg.Traceparent)
	ctx, turnSpan := obs.StartLinked(ctx, "turn", msg.MergedTraceparents,
		obs.Str("session_key", t.sessionKey),
		obs.Str("source", string(msg.Source)),
		obs.Int("merged_count", len(msg.MergedTraceparents)+1),
		// How long this wake sat in the inbox. The one number that separates
		// "the model was slow" from "we were busy answering someone else".
		obs.Int64("wait_ms", queueWaitMs(msg.EnqueuedAt)),
	)
	defer turnSpan.End()

	t.lastWakeSource = msg.Source
	// Per-wake model override (from dispatch subagent/fork). Reset every wake —
	// an empty wake clears any prior override so it never leaks across turns.
	t.modelOverrideProvider = msg.OverrideProvider
	t.modelOverrideModel = msg.OverrideModel
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
		// No explicit agent override in the wake message — hot-reload from
		// meta.json (set-agent / manual edit). DefaultAgentFor reads meta.json
		// each call, falling back to "soul" if empty.
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

	sink := resolveTurnSinks(msg.Source, msg.Sinks, t.defaultSink)

	deliveryLabel := t.resolveDeliveryLabel(msg.Source, sink)

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
	// Recorded here rather than at span open: the agent is hot-reloaded and the
	// model resolved above, so opening the span with them would report the
	// previous turn's routing.
	turnSpan.Set(
		obs.Str("agent", agentName),
		obs.Str("provider", prov),
		obs.Str("model", mod),
	)
	sender := senderOrDefault(msg.Sender, msg.Source)
	userMessage := buildWakePayload(msg.Source, msg.Message, t.id, t.sessionKey, sessionDir, deliveryLabel, modelLabel, agentName, loc, sender, msg.SenderName, msg.SenderID, msg.MediaInfo, msg.MediaPreview, msg.CallerSessionKey, msg.EnqueuedAt, msg.RecentChat)

	// Build injection function: between tool iterations, drain inbox for
	// mergeable user messages and inject them into the LLM conversation.
	// Non-mergeable messages are stored in t.pending to avoid channel
	// requeue deadlock.
	var defaultAgent func() string
	if fn := t.cfg().DefaultAgentFor; fn != nil {
		defaultAgent = func() string { return fn(t.sessionKey) }
	}
	injectFn := func() []provider.Message {
		var injected []provider.Message
		// Control lane first: drain the dedicated inject queue unconditionally
		// (no canMerge gate). Wrapped as injected system messages so they are
		// visible in history but skipped by the resume scanner.
	controlLane:
		for {
			select {
			case body := <-t.injectInbox:
				payload := markInjected(sysmsg.BuildSystemMessage("injected_control", nil, body))
				injected = append(injected, provider.UserMessage(payload))
				logger.Info("injected control message",
					"threadID", t.id,
					"sessionKey", t.sessionKey,
				)
			default:
				break controlLane
			}
		}
		for {
			select {
			case next := <-t.inbox:
				if reason := injectRejectReason(msg, next, agentName, defaultAgent); reason == "" {
					payload := buildWakePayload(next.Source, next.Message, t.id, t.sessionKey, sessionDir, deliveryLabel, modelLabel, agentName, loc, senderOrDefault(next.Sender, next.Source), next.SenderName, next.SenderID, next.MediaInfo, next.MediaPreview, next.CallerSessionKey, next.EnqueuedAt, next.RecentChat)
					if payload != "" {
						payload = markInjected(payload)
						// An injected message IS its own persisted entry (it is
						// not folded into the turn's user message), so it keeps
						// the sender's own id.
						injectedMsg := provider.UserMessage(payload)
						injectedMsg.ID = next.ID
						injected = append(injected, injectedMsg)
						logger.Info("injected mid-execution message",
							"threadID", t.id,
							"sessionKey", t.sessionKey,
							"source", next.Source,
						)
					}
				} else {
					// A deferred wake waits for the whole running turn to end, which
					// for a steer is the difference between correcting the work and
					// arriving after it. Never silent.
					logger.Warn("wake deferred instead of injected",
						"threadID", t.id,
						"sessionKey", t.sessionKey,
						"source", next.Source,
						"reason", reason,
					)
					t.pending = append(t.pending, next) // not mergeable, defer
					return injected
				}
			default:
				return injected
			}
		}
	}

	t.mu.Lock()
	t.lastTurnMetrics = nil
	t.mu.Unlock()
	response, err := t.run(ctx, userMessage, msg.ID, msg.Media, sink, msg.CallerSink, msg.MessageSink, msg.CallerSessionKey, injectFn, string(msg.Source))
	if t.mgr != nil {
		t.mgr.notifyTurnEnd(t.turnEnd(ctx, msg.Source, response, err))
	}

	// Run post-turn hooks BEFORE consuming the per-turn flags so hooks see
	// the state accurately. Returned strings are persisted as user-role
	// messages and become visible to subsequent turns.
	t.persistPostInjections(t.runPostHooks(ctx, postTurnContext{
		ThreadID:         t.id,
		SessionKey:       t.sessionKey,
		WakeSource:       msg.Source,
		CallerSessionKey: msg.CallerSessionKey,
		IsUserFacing:     t.IsUserFacing(),
		FinalReply:       response,
	}), msg.Source, sink)

	t.checkAndResetSinkSuppressed()

	if err != nil {
		turnSpan.Fail(err)
		logger.Error("thread run error", "threadID", t.id, "sessionKey", t.sessionKey, "source", msg.Source, "err", err)
		errMsg := sysmsg.BuildSystemMessage("error", nil, fmt.Sprintf("%v", err))
		if !sink.IsZero() {
			if sinkErr := sink.WithRetry(3).Send(ctx, errMsg); sinkErr != nil {
				logger.Error("sink delivery error", "threadID", t.id, "sessionKey", t.sessionKey, "err", sinkErr)
			}
		}
	}

	if flushErr := sink.Flush(ctx); flushErr != nil {
		logger.Warn("sink flush failed", "threadID", t.id, "sessionKey", t.sessionKey, "err", flushErr)
	}

	if msg.OnComplete != nil {
		msg.OnComplete(response)
	}
}

// turnEnd snapshots the turn that just finished for the turn-end hook. The
// metrics are the ones run() retired at its end; a turn that never started
// (empty wake body) leaves them nil and yields an empty Info.
func (t *Thread) turnEnd(ctx context.Context, source WakeSource, response string, err error) TurnEnd {
	te := TurnEnd{
		SessionKey:  t.sessionKey,
		Source:      source,
		FinalReply:  response,
		Err:         err,
		Traceparent: obs.Traceparent(ctx),
	}
	t.mu.Lock()
	m := t.lastTurnMetrics
	t.mu.Unlock()
	if m == nil {
		return te
	}
	m.mu.Lock()
	te.Info = sysmsg.ThreadInfo{
		SessionKey:     t.sessionKey,
		Iterations:     m.Iterations,
		TotalToolCalls: m.TotalToolCalls,
		ElapsedSec:     int(time.Since(m.TurnStart).Seconds()),
		ToolTrace:      append([]ToolCallRecord(nil), m.ToolCalls...),
		TurnWakeSource: string(source),
		OriginRequest:  m.OriginRequest,
		TurnStart:      m.TurnStart,
	}
	m.mu.Unlock()
	return te
}

// buildWakePayload constructs the user message from a wake source and message.
// Uses YAML frontmatter + markdown body so the AI knows the wake context
// and the sender (user vs system).
//
// recentChat is rendered into the markdown body (not YAML frontmatter) so long
// chat history stays out of metadata.
func buildWakePayload(source WakeSource, message, threadID, sessionKey, sessionDir, deliveryLabel, model, agent string, loc *time.Location, sender, senderName, senderID, mediaInfo, mediaPreview, callerSessionKey string, enqueuedAt time.Time, recentChat string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	if source == "" {
		source = "unknown"
	}

	if enqueuedAt.IsZero() {
		enqueuedAt = time.Now()
	}
	now := enqueuedAt.In(loc)

	delivery := deliveryLabel
	if delivery == "" {
		delivery = fallbackDeliveryLabel(source)
	}

	header := wakeHeader{
		Source:           string(source),
		Thread:           threadID,
		Session:          sessionKey,
		SessionDir:       sessionDir,
		Time:             formatWakeTime(now),
		Model:            model,
		Agent:            agent,
		Delivery:         delivery,
		Sender:           sender,
		SenderName:       senderName,
		SenderID:         senderID,
		Media:            oneLine(mediaInfo),
		MediaPreview:     oneLine(mediaPreview),
		CallerSessionKey: callerSessionKey,
	}
	hint := wakeActionHint(source)
	if hint != "" {
		header.Action = hint
	}
	// Include multimodal capabilities when the model supports them.
	// Only set fields for true capabilities — false is the default and omitted.
	if model != "" {
		if prov, mod, ok := strings.Cut(model, "/"); ok {
			if provider.SupportsVision(prov, mod) {
				v := true
				header.SupportsVision = &v
			}
			if provider.SupportsAudio(prov, mod) {
				a := true
				header.SupportsAudio = &a
			}
			if provider.SupportsPDF(prov, mod) {
				p := true
				header.SupportsPDF = &p
			}
		}
	}

	body := "\n" + message
	if strings.TrimSpace(recentChat) != "" {
		body = buildWakeContextBody(recentChat, message, hint)
	}

	mapping, ok := sysmsg.EncodeMapping(header)
	if !ok {
		return ""
	}
	return sysmsg.BuildFrontmatter(mapping, body)
}

func buildWakeContextBody(history, message, instruction string) string {
	bodyInstruction := promptHistoryInstruction.Text()
	if strings.TrimSpace(instruction) == "" {
		bodyInstruction = promptHistoryInstructionNoAction.Text()
	}
	sections := []string{
		"## history",
		strings.TrimSpace(history),
		"## message",
		strings.TrimSpace(message),
		"## instruction",
		bodyInstruction,
	}
	return "\n" + strings.Join(sections, "\n\n")
}

// wakeHeader is the YAML frontmatter for wake messages.
type wakeHeader struct {
	Source           string `yaml:"source"`
	Thread           string `yaml:"thread"`
	Session          string `yaml:"session"`
	SessionDir       string `yaml:"session_dir,omitempty"`
	Time             string `yaml:"time"`
	Model            string `yaml:"model,omitempty"`
	Agent            string `yaml:"agent,omitempty"`
	Delivery         string `yaml:"delivery"`
	Sender           string `yaml:"sender"`
	SenderName       string `yaml:"sender_name,omitempty"`
	SenderID         string `yaml:"sender_id,omitempty"`
	Media            string `yaml:"media,omitempty"`
	MediaPreview     string `yaml:"media_preview,omitempty"`
	CallerSessionKey string `yaml:"caller_session_key,omitempty"`
	Action           string `yaml:"action,omitempty"`
	SupportsVision   *bool  `yaml:"supports_vision,omitempty"`
	SupportsAudio    *bool  `yaml:"supports_audio,omitempty"`
	SupportsPDF      *bool  `yaml:"supports_pdf,omitempty"`
}

// oneLine collapses every run of whitespace (including newlines) into a single
// space, so multi-line channel media summaries and previews fit a one-line
// frontmatter value.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// joinNonEmpty joins two optional values with a separator, skipping empties.
func joinNonEmpty(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + " | " + b
}

// formatWakeTime renders a timestamp in the format used by wake frontmatter
// (`RFC3339 (Weekday, Location, UTC±HH:MM)`). Shared between buildWakePayload
// and post-turn injections so the two paths stay consistent.
func formatWakeTime(now time.Time) string {
	return fmt.Sprintf("%s (%s, %s, UTC%s)", now.Format(time.RFC3339), now.Weekday(), now.Location(), now.Format("-07:00"))
}

// markInjected adds `injected: true` to the YAML frontmatter of a wake
// payload. This marks messages that were injected mid-execution (via injectFn)
// rather than initiating a new reasoning turn. Returns the payload unchanged
// when there is no parseable frontmatter.
func markInjected(payload string) string {
	mapping, body, ok := sysmsg.ParseFrontmatter(payload)
	if !ok {
		return payload
	}
	sysmsg.AppendScalarPair(mapping, "injected", "true")
	return sysmsg.BuildFrontmatter(mapping, body)
}

// messageSender returns the sender label for a wake source.
// User-originated messages are "user"; system messages are "system".
func messageSender(source WakeSource) string {
	if sysmsg.IsUserVisibleSource(source) {
		return "user"
	}
	return "system"
}

func senderOrDefault(override string, source WakeSource) string {
	if override != "" {
		return override
	}
	return messageSender(source)
}

// wakeActionHint returns the `action` field for a wake from source. The text
// of each is in prompts/templates/wake/; promptWakeUser documents why the
// channel-user guidance rides the wake rather than the system prompt.
func wakeActionHint(source WakeSource) string {
	if sysmsg.IsUserVisibleSource(source) {
		return promptWakeUser.Text()
	}
	switch source {
	case WakeSession:
		return promptWakeSession.Text()
	case WakeCron:
		// A cron wake into a user-facing session is proactive speech: the task
		// text is often the model's own follow-up note (dream step 7), which the
		// human never saw. The guidance says what the opening must DO (give the
		// context the reader lacks) rather than forbidding mentions of the
		// schedule — the old "do not mention the schedule" line pushed replies
		// toward bare answers ("有的，有打折") to a question nobody asked.
		// That guidance lives in the wake-and-delivery section; the action
		// only points at it, like progress and session, because an action
		// stays on every past wake until idle Tier-1 compression strips it.
		return promptWakeCron.Text()
	case WakeCompression:
		return promptWakeCompression.Text()
	case WakeHeartbeat:
		return promptWakeHeartbeat.Text()
	case WakeResume:
		return promptWakeResume.Text()
	case WakeAudioPreview:
		return promptWakeAudioPreview.Text()
	case WakeImagePreview:
		return promptWakeImagePreview.Text()
	case WakeProgress:
		return promptWakeProgress.Text()
	case WakeProgressSum:
		return promptWakeProgressSummary.Text()
	case WakeQuote:
		return promptWakeQuote.Text()
	default:
		return promptWakeDefault.Text()
	}
}
