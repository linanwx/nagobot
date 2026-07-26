package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/auth"
	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/media"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread"
	sysmsg "github.com/linanwx/nagobot/thread/msg"
)

// Dispatcher routes channel messages to threads. It is the bridge between
// the channel layer (pure I/O) and the thread layer (async execution).
type Dispatcher struct {
	channels *channel.Manager
	threads  *thread.Manager
	cfg      *config.Config
	ctx      context.Context
	authMgr  *auth.Manager // records channel identities; nil in tests
}

// NewDispatcher creates a new dispatcher.
func NewDispatcher(
	channels *channel.Manager,
	threads *thread.Manager,
	cfg *config.Config,
) *Dispatcher {
	return &Dispatcher{
		channels: channels,
		threads:  threads,
		cfg:      cfg,
	}
}

// Run starts a goroutine for each channel that reads messages and dispatches
// them to threads. Blocks until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	d.ctx = ctx
	d.channels.Each(func(ch channel.Channel) {
		go d.processChannel(ctx, ch)
	})
	<-ctx.Done()
}

// StartDispatching begins dispatching for a dynamically added channel.
func (d *Dispatcher) StartDispatching(ch channel.Channel) {
	go d.processChannel(d.ctx, ch)
}

func (d *Dispatcher) processChannel(ctx context.Context, ch channel.Channel) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch.Messages():
			if !ok {
				return
			}
			d.dispatch(ctx, ch, msg)
		}
	}
}

func (d *Dispatcher) dispatch(ctx context.Context, ch channel.Channel, msg *channel.Message) {
	logger.Debug("dispatching message",
		"channel", ch.Name(),
		"channelID", msg.ChannelID,
		"user", msg.Username,
		"text", truncate(msg.Text, 50),
	)

	// Intercept /init command — execute directly, bypass LLM.
	if text := strings.TrimSpace(msg.Text); strings.HasPrefix(text, "/init") {
		d.handleInit(ctx, ch, msg, text)
		return
	}

	sessionKey := d.route(msg)
	if sd, err := d.cfg.SessionsDir(); err == nil {
		persistChannelRouting(sd, sessionKey, msg)
	}
	sink, callerSink, msgSink := d.buildSinks(ch, msg, sessionKey)
	agentName, vars := d.resolveAgentName(sessionKey, msg)
	userMessage := d.preprocessMessage(msg)
	source := d.wakeSource(ch)
	senderName := senderDisplayName(msg)

	// Stable sender identity for rendering attribution. Chat channels use
	// channel:userID (matches the identity dictionary / person bindings);
	// authenticated web users are attributed to their person. Exempt web
	// connections and channels without user IDs stay unattributed.
	senderID := ""
	if ch.Name() == "web" {
		if pid := msg.Metadata["person_id"]; pid != "" {
			senderID = "person:" + pid
		}
	} else if msg.UserID != "" {
		senderID = ch.Name() + ":" + msg.UserID
	}

	// Media summary + upfront preview ride the wake frontmatter (`media` /
	// `media_preview`), keeping the markdown body pure user speech.
	mediaInfo := msg.Metadata["media_summary"]
	mediaPreview := ""
	if mediaInfo != "" {
		mediaPreview = d.generateMediaPreviews(sessionKey, mediaInfo)
	}

	// Persist a client-reported timezone (web only, already validated at the
	// channel boundary) so every wake for this session — including later cron
	// and heartbeat wakes that have no client attached — renders its frontmatter
	// time in the human's device zone. Only the zone is learned; the timestamp
	// value stays the server clock.
	if tz := msg.Metadata["client_tz"]; tz != "" {
		if dir := d.threads.SessionDir(sessionKey); dir != "" {
			session.UpdateMeta(dir, func(m *session.Meta) { m.ClientTimezone = tz })
		}
	}

	if sysmsg.IsUserVisibleSource(source) {
		// Feed the channel-identity dictionary (web login "claim your
		// discord:Nansen" flow) — only real chat-channel speech counts.
		// The web console is excluded: an authenticated web user already IS
		// a person, and recording it would fabricate junk identities like
		// "web:discord:<session>" from its bound-session UserID.
		if ch.Name() != "web" {
			d.authMgr.RecordIdentity(ch.Name(), msg.UserID, senderName)
		}
		if err := session.AppendChat(d.threads.SessionDir(sessionKey), session.ChatRoleUser, senderName, userMessage, time.Now()); err != nil {
			logger.Warn("chat.jsonl user-write failed", "sessionKey", sessionKey, "err", err)
		}
	}

	d.threads.Wake(sessionKey, &thread.WakeMessage{
		Source:       source,
		Message:      userMessage,
		ID:           msg.Metadata["client_msg_id"], // already validated at the channel boundary; empty for channels that don't mint one
		Sinks:        sink,
		CallerSink:   callerSink,
		MessageSink:  msgSink,
		AgentName:    agentName,
		Vars:         vars,
		SenderName:   senderName,
		SenderID:     senderID,
		MediaInfo:    mediaInfo,
		MediaPreview: mediaPreview,
	})
}

// handleInit intercepts /init messages and executes the init command directly.
func (d *Dispatcher) handleInit(ctx context.Context, ch channel.Channel, msg *channel.Message, text string) {
	args := strings.Fields(text)
	if len(args) > 0 {
		args = args[1:] // remove "/init"
	}

	var buf bytes.Buffer
	initCmd.SetOut(&buf)
	initCmd.SetErr(&buf)

	// Parse flags directly and call RunE — avoid Execute() which
	// traverses to root and re-runs the parent command (e.g. serve).
	var response string
	if err := initCmd.ParseFlags(args); err != nil {
		response = fmt.Sprintf("Error: %v", err)
	} else if err := initCmd.RunE(initCmd, initCmd.Flags().Args()); err != nil {
		response = fmt.Sprintf("Error: %v", err)
	} else {
		response = buf.String()
		if strings.TrimSpace(response) == "" {
			response = "Configuration saved."
		}
	}

	sink, _, _ := d.buildSinks(ch, msg, d.route(msg))
	if !sink.IsZero() {
		_ = sink.Send(ctx, response)
	}
}

// chatGroupTypes defines which chat_type values count as group chats per channel prefix.
var chatGroupTypes = map[string][]string{
	"telegram:": {"group", "supergroup"},
	"feishu:":   {"group"},
	"discord:":  {"group"},
	"wecom:":    {"group"},
}

// route determines the session key for a message.
func (d *Dispatcher) route(msg *channel.Message) string {
	if msg == nil {
		return "cli"
	}

	if msg.ChannelID == "cli:local" || strings.HasPrefix(msg.ChannelID, "socket:") {
		return "cli"
	}

	// Web channel: "web:main" and "web:cli" → "cli"; "web:{sessionKey}" → route to that session.
	if suffix, ok := strings.CutPrefix(msg.ChannelID, "web:"); ok {
		if suffix == "" || suffix == "main" || suffix == "cli" {
			return "cli"
		}
		return suffix
	}

	// Chat channels (telegram, feishu, discord): group → shared session, else → per-user.
	for prefix, groupTypes := range chatGroupTypes {
		if strings.HasPrefix(msg.ChannelID, prefix) {
			return d.routeChatChannel(msg, prefix, groupTypes)
		}
	}

	if strings.HasPrefix(msg.ChannelID, "cron:") {
		jobID := strings.TrimSpace(msg.Metadata["job_id"])
		if jobID == "" {
			jobID = "job"
		}
		return "cron:" + jobID
	}

	sessionKey := msg.ChannelID
	if msg.UserID != "" {
		sessionKey = msg.ChannelID + ":" + msg.UserID
	}
	return sessionKey
}

// routeChatChannel routes a chat channel message to a session key.
// Group chats share a session by channel ID; DMs use per-user keys.
func (d *Dispatcher) routeChatChannel(msg *channel.Message, prefix string, groupTypes []string) string {
	chatType := strings.TrimSpace(msg.Metadata["chat_type"])
	for _, gt := range groupTypes {
		if chatType == gt {
			return msg.ChannelID
		}
	}
	userID := strings.TrimSpace(msg.UserID)
	if userID != "" {
		return prefix[:len(prefix)-1] + ":" + userID // e.g. "telegram:" → "telegram" + ":" + userID
	}
	return msg.ChannelID
}

// buildSinks creates the per-wake delivery for a channel message: the sink for
// the channel the message ARRIVED on, plus the message-specific sink that can
// react on the very message that woke us.
//
// It deliberately knows nothing about the session's own destinations. RunOnce
// unions this over them, so a message reaching a session from a foreign channel
// answers on both without either side of the wiring having to enumerate the
// other.
func (d *Dispatcher) buildSinks(ch channel.Channel, msg *channel.Message, sessionKey string) (thread.SinkSet, thread.SessionSink, thread.MessageSink) {
	if ch.Name() == "cron" {
		return thread.SinkSet{}, d.buildCronSink(msg), thread.MessageSink{}
	}

	manager := d.channels
	if manager == nil || msg == nil {
		return thread.SinkSet{}, thread.SessionSink{}, thread.MessageSink{}
	}

	channelName := ch.Name()
	replyTo := strings.TrimSpace(msg.Metadata["chat_id"])
	if replyTo == "" {
		replyTo = strings.TrimSpace(msg.ReplyTo)
	}

	// chat.jsonl recording: accumulate every Send into a per-turn buffer; flush
	// at end-of-turn writes one assistant entry. The buffer is reset on flush
	// so the same sink can serve sequential turns. Mutex guards against the
	// (theoretical) case of overlapping callers — Threads are single-RunOnce
	// at a time, but the sink may be reused across sequential turns.
	sessionDir := d.threads.SessionDir(sessionKey)
	var bufMu sync.Mutex
	var buf strings.Builder

	sink := thread.SessionSink{
		Channel: channelName,
		Label:   "your response will be sent to the user via " + channelName,
		Send: func(ctx context.Context, response string) error {
			if strings.TrimSpace(response) == "" {
				return nil
			}
			bufMu.Lock()
			buf.WriteString(response)
			bufMu.Unlock()
			return manager.SendTo(ctx, channelName, response, replyTo)
		},
		Flush: func(ctx context.Context) error {
			bufMu.Lock()
			content := buf.String()
			buf.Reset()
			bufMu.Unlock()
			if strings.TrimSpace(content) == "" {
				return nil
			}
			if err := session.AppendChat(sessionDir, session.ChatRoleAssistant, "", content, time.Now()); err != nil {
				logger.Warn("chat.jsonl assistant-write failed", "sessionKey", sessionKey, "err", err)
			}
			return nil
		},
	}

	// Rich streaming: channels implementing Streamer (web) get live thinking/
	// text deltas and tool events through a pipe — Push never blocks the turn;
	// a lazily-started drain goroutine forwards to the bound client. Flushing
	// the pipe before every authoritative Send keeps frame order: the client
	// never sees a final response overtaken by stale deltas.
	if _, ok := ch.(channel.Streamer); ok {
		pipe := thread.NewStreamPipe(func(ev thread.StreamEvent) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := manager.StreamTo(ctx, channelName, replyTo, ev); err != nil {
				logger.Debug("stream event delivery failed", "channel", channelName, "err", err)
			}
		})
		innerSend := sink.Send
		sink.Stream = pipe.Push
		sink.Send = func(ctx context.Context, response string) error {
			pipe.Flush()
			return innerSend(ctx, response)
		}
		// A streaming channel renders the turn live and takes no chunks: its
		// deltas already deliver the text, and the persisted entries arrive as
		// message frames. Registering both — which is what the old Chunkable
		// bool did on web — sent every intermediate assistant message a second
		// time, producing a redundant push per tool round.
	} else {
		sink = sink.Chunked()
	}

	return thread.NewSinks(sink), thread.SessionSink{}, thread.MessageSink{
		React: d.buildReactFunc(channelName, manager, msg),
	}
}

// buildCronSink returns a drop sink for cron-channel messages.
// Cron-triggered turns must explicitly dispatch() to deliver output; naive
// text output is discarded. This path is the legacy channel-message fallback;
// the primary path is onDirectWake in serve.go, which also uses a drop sink.
func (d *Dispatcher) buildCronSink(msg *channel.Message) thread.SessionSink {
	reportTo := ""
	if msg != nil {
		reportTo = strings.TrimSpace(msg.Metadata["wake_session"])
	}
	var label string
	if reportTo != "" {
		label = "cron caller output is dropped. After completing your task, dispatch(to=session, params={session_key: \"" + reportTo + "\"}) to deliver results."
	} else {
		label = "cron caller output is dropped. No delivery target configured; use dispatch explicitly if you need to forward results."
	}
	return thread.SessionSink{
		Label: label,
		Send: func(_ context.Context, response string) error {
			if strings.TrimSpace(response) != "" {
				logger.Debug("cron dispatcher sink dropped", "bytes", len(response))
			}
			return nil
		},
	}
}

// Per-platform emoji mapping for ReactEvents.
var platformEmoji = map[string]map[thread.ReactEvent]string{
	"telegram": {thread.ReactToolCalls: "⚡", thread.ReactStreaming: "✍"},
	"discord":  {thread.ReactToolCalls: "🔧", thread.ReactStreaming: "✏️"},
}

// defaultEmoji is used for CLI/socket/web debugging.
var defaultEmoji = map[thread.ReactEvent]string{
	thread.ReactToolCalls: "🔧",
	thread.ReactStreaming:  "✏️",
}

func emojiFor(channelName string, event thread.ReactEvent) string {
	if m, ok := platformEmoji[channelName]; ok {
		if e, ok := m[event]; ok {
			return e
		}
	}
	if e, ok := defaultEmoji[event]; ok {
		return e
	}
	return ""
}

// buildReactFunc creates a ReactFunc for a channel message.
// Each platform maps ReactEvents to its own emoji set.
func (d *Dispatcher) buildReactFunc(channelName string, manager *channel.Manager, msg *channel.Message) thread.ReactFunc {
	if msg == nil {
		return thread.ReactFunc{}
	}
	msgID := msg.ID
	chatID := strings.TrimSpace(msg.Metadata["chat_id"])
	if chatID == "" {
		chatID = strings.TrimSpace(msg.ReplyTo)
	}

	// CLI/socket/web: print to stderr for testing.
	if channelName == "cli" || channelName == "socket" || channelName == "web" {
		return thread.NewReactFunc(func(_ context.Context, event thread.ReactEvent) {
			if emoji := emojiFor(channelName, event); emoji != "" {
				fmt.Fprintf(os.Stderr, "[react] %s\n", emoji)
			}
		})
	}

	// Channels with Reactor support (telegram, discord, etc.).
	if chatID != "" && msgID != "" {
		return thread.NewReactFunc(func(ctx context.Context, event thread.ReactEvent) {
			if emoji := emojiFor(channelName, event); emoji != "" {
				_ = manager.ReactTo(ctx, channelName, chatID, msgID, emoji)
			}
		})
	}
	return thread.ReactFunc{}
}

// resolveAgentName returns the agent name and vars for a message.
// It checks msg metadata first, then looks up the session key in sessionAgents.
// Empty name means use the default (soul) agent.
func (d *Dispatcher) resolveAgentName(sessionKey string, msg *channel.Message) (string, map[string]string) {
	if msg == nil {
		return "", nil
	}

	agentName := strings.TrimSpace(msg.Metadata["agent"])
	if agentName == "" {
		agentName = session.MetaAgent(d.threads.SessionDir(sessionKey))
	}
	if agentName == "" {
		return "", nil
	}

	var vars map[string]string
	if task := strings.TrimSpace(msg.Metadata["task"]); task != "" {
		vars = map[string]string{"TASK": task}
	}
	return agentName, vars
}

// preprocessMessage prepends reply context, sender name, and thread header to
// the user message. Media summary and previews are NOT inlined here — they
// travel as WakeMessage.MediaInfo / MediaPreview and render as `media` /
// `media_preview` in the wake frontmatter.
func (d *Dispatcher) preprocessMessage(msg *channel.Message) string {
	text := msg.Text

	// Prepend quoted reply context so the AI knows what message was replied to.
	if rc := msg.Metadata["reply_context"]; rc != "" {
		text = truncate(rc, 500) + "\n\n" + text
	}

	// For group chats, prepend sender name so the AI can distinguish players.
	// The name is also carried structurally as `sender_name` in the wake
	// frontmatter and chat.jsonl; the inline prefix stays because merged wake
	// bodies need per-message attribution.
	chatType := strings.TrimSpace(msg.Metadata["chat_type"])
	if chatType == "group" || chatType == "supergroup" {
		if sender := senderDisplayName(msg); sender != "" {
			text = "[" + sender + "]: " + text
		}
	}

	// For Discord thread / forum-post messages, prepend a header with post title
	// and applied tags so the LLM keeps the topic in focus on every turn.
	if header := threadHeader(msg.Metadata); header != "" {
		text = header + "\n" + text
	}

	return text
}

// senderDisplayName returns the human display name of a message's sender
// (username, falling back to first_name metadata). Empty when the channel
// provides neither (e.g. cron / CLI messages).
func senderDisplayName(msg *channel.Message) string {
	if msg == nil {
		return ""
	}
	if s := strings.TrimSpace(msg.Username); s != "" {
		return s
	}
	return strings.TrimSpace(msg.Metadata["first_name"])
}

// threadHeader formats a one-line context header for Discord thread / forum
// messages. Returns "" when the message has no thread metadata.
func threadHeader(meta map[string]string) string {
	name := strings.TrimSpace(meta["thread_name"])
	threadType := strings.TrimSpace(meta["thread_type"])
	if name == "" && threadType == "" {
		return ""
	}

	label := "Thread"
	if threadType == "forum_post" {
		label = "Forum post"
	}

	var b strings.Builder
	b.WriteString("[")
	b.WriteString(label)
	if name != "" {
		b.WriteString(" \"")
		b.WriteString(name)
		b.WriteString("\"")
	}
	if forumName := strings.TrimSpace(meta["forum_name"]); forumName != "" && threadType == "forum_post" {
		b.WriteString(" in #")
		b.WriteString(forumName)
	}
	if tags := strings.TrimSpace(meta["applied_tags"]); tags != "" {
		b.WriteString(" · tags: ")
		b.WriteString(tags)
	}
	b.WriteString("]")
	return b.String()
}

// mediaPathRe matches "image_path: /path" or "audio_path: /path" lines in media summaries.
var mediaPathRe = regexp.MustCompile(`(?m)^(image_path|audio_path):\s*(.+)$`)

// generateMediaPreviews extracts media file paths from a media summary string
// and returns formatted preview tags. Both image and audio are previewed by
// stateless preview agents (image-preview / audio-preview) woken on the
// session's :imagepreview / :audiopreview sibling with the file attached
// natively. An empty result (variant not configured, or the turn failed —
// logged inside the preview call) skips that tag and lets the main turn fall
// back to read_file/imagereader/audioreader. Returns "" if nothing previewed.
func (d *Dispatcher) generateMediaPreviews(sessionKey, mediaSummary string) string {
	matches := mediaPathRe.FindAllStringSubmatch(mediaSummary, -1)
	if len(matches) == 0 {
		return ""
	}

	var previews []string
	for _, m := range matches {
		pathType := m[1] // "image_path" or "audio_path"
		filePath := strings.TrimSpace(m[2])
		if filePath == "" {
			continue
		}

		if pathType == "audio_path" {
			marker := fmt.Sprintf("<<media:%s:%s>>", media.DetectAudioMime(filePath), filePath)
			if transcription := d.threads.AudioPreview(d.ctx, sessionKey, marker); transcription != "" {
				previews = append(previews, media.FormatPreviewTag(transcription, media.MediaTypeAudio))
			}
			continue
		}

		// image_path
		marker := fmt.Sprintf("<<media:%s:%s>>", media.DetectImageMime(filePath), filePath)
		if description := d.threads.ImagePreview(d.ctx, sessionKey, marker); description != "" {
			previews = append(previews, media.FormatPreviewTag(description, media.MediaTypeImage))
		}
	}

	if len(previews) == 0 {
		return ""
	}
	return strings.Join(previews, "\n")
}

// wakeSource returns the wake source for a channel.
func (d *Dispatcher) wakeSource(ch channel.Channel) thread.WakeSource {
	return thread.WakeSource(ch.Name())
}

// persistChannelRouting writes channel routing metadata to meta.json for
// channels that need routing info beyond what the session key provides
// (e.g., Discord DM needs "dm:{userID}" to create a DM channel on send,
// WeCom needs req_id to reply after service restart).
func persistChannelRouting(sessionsDir, sessionKey string, msg *channel.Message) {
	if msg == nil {
		return
	}

	sessionDir := session.SessionDir(sessionsDir, sessionKey)

	// Discord DM: persist reply_to for DM channel creation.
	chatType := strings.TrimSpace(msg.Metadata["chat_type"])
	if chatType == "dm" && strings.HasPrefix(msg.ChannelID, "discord:") {
		if userID := strings.TrimSpace(msg.UserID); userID != "" {
			session.UpdateMeta(sessionDir, func(m *session.Meta) {
				m.DiscordDM = &session.DiscordDMMeta{
					ReplyTo: "dm:" + userID,
					UserID:  userID,
				}
			})
		}
	}

}

// truncate shortens s to at most maxLen runes. It prefers cutting at a
// sentence boundary (newline or common punctuation) within the last 20% of the
// limit; otherwise it cuts at the rune boundary.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	// Look for a sentence break in the tail 20% of the window.
	searchFrom := maxLen - maxLen/5
	best := -1
	for i := maxLen - 1; i >= searchFrom; i-- {
		switch runes[i] {
		case '\n', '.', '。', '！', '？', '!', '?':
			best = i + 1
		}
		if best > 0 {
			break
		}
	}
	if best <= 0 {
		best = maxLen
	}
	return string(runes[:best]) + "..."
}
