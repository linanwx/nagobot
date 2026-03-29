package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
	channels  *channel.Manager
	threads   *thread.Manager
	cfg       *config.Config
	ctx       context.Context
	previewer media.Previewer
}

// NewDispatcher creates a new dispatcher.
func NewDispatcher(
	channels *channel.Manager,
	threads *thread.Manager,
	cfg *config.Config,
) *Dispatcher {
	return &Dispatcher{
		channels:  channels,
		threads:   threads,
		cfg:       cfg,
		previewer: media.NewPreviewer(func() *config.Config {
			cfg, err := config.Load()
			if err != nil {
				return nil
			}
			return cfg
		}),
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
	sink := d.buildSink(ch, msg)
	agentName, vars := d.resolveAgentName(sessionKey, msg)
	userMessage := d.preprocessMessage(msg)
	source := d.wakeSource(ch)

	d.threads.Wake(sessionKey, &thread.WakeMessage{
		Source:    source,
		Message:   userMessage,
		Sink:      sink,
		AgentName: agentName,
		Vars:      vars,
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

	sink := d.buildSink(ch, msg)
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

// buildSink creates a per-wake sink that delivers the response back to the
// originating channel.
func (d *Dispatcher) buildSink(ch channel.Channel, msg *channel.Message) thread.Sink {
	if ch.Name() == "cron" {
		return d.buildCronSink(msg)
	}

	manager := d.channels
	if manager == nil || msg == nil {
		return thread.Sink{}
	}

	channelName := ch.Name()
	replyTo := strings.TrimSpace(msg.Metadata["chat_id"])
	if replyTo == "" {
		replyTo = strings.TrimSpace(msg.ReplyTo)
	}

	sink := thread.Sink{
		Label:     "your response will be sent to the user via " + channelName,
		Chunkable: true,
		Send: func(ctx context.Context, response string) error {
			if strings.TrimSpace(response) == "" {
				return nil
			}
			return manager.SendTo(ctx, channelName, response, replyTo)
		},
	}

	// Build React closure for channels that support it.
	sink.React = d.buildReactFunc(channelName, manager, msg)
	return sink
}

// buildCronSink creates a sink for cron jobs that wakes the creator thread
// with the result.
func (d *Dispatcher) buildCronSink(msg *channel.Message) thread.Sink {
	if msg == nil {
		return thread.Sink{}
	}

	reportTo := strings.TrimSpace(msg.Metadata["wake_session"])
	jobID := strings.TrimSpace(msg.Metadata["job_id"])

	if reportTo == "" {
		return thread.Sink{Label: "cron silent, result will not be delivered"}
	}

	return thread.Sink{
		Label: "your task will be injected into session " + reportTo + " which will wake, execute, and deliver the result to the user",
		Send: func(ctx context.Context, response string) error {
			if strings.TrimSpace(response) == "" {
				return nil
			}
			wakeMsg := sysmsg.BuildSystemMessage("cron_completed", map[string]string{
				"id": jobID,
			}, strings.TrimSpace(response))
			d.threads.Wake(reportTo, &thread.WakeMessage{
				Source:  thread.WakeCronFinished,
				Message: wakeMsg,
			})
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
		agentName = d.cfg.SessionAgent(sessionKey)
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

// preprocessMessage prepends media summary, previews, and sender name to the user message.
func (d *Dispatcher) preprocessMessage(msg *channel.Message) string {
	text := msg.Text

	mediaSummary := msg.Metadata["media_summary"]
	if mediaSummary != "" {
		// Generate fast media previews for downloaded media files.
		previews := d.generateMediaPreviews(mediaSummary)
		if previews != "" {
			text = previews + "\n\n" + mediaSummary + "\n\n" + text
		} else {
			text = mediaSummary + "\n\n" + text
		}
	}

	// Prepend quoted reply context so the AI knows what message was replied to.
	if rc := msg.Metadata["reply_context"]; rc != "" {
		text = truncate(rc, 500) + "\n\n" + text
	}

	// For group chats, prepend sender name so the AI can distinguish players.
	chatType := strings.TrimSpace(msg.Metadata["chat_type"])
	if chatType == "group" || chatType == "supergroup" {
		sender := strings.TrimSpace(msg.Username)
		if sender == "" {
			sender = strings.TrimSpace(msg.Metadata["first_name"])
		}
		if sender != "" {
			text = "[" + sender + "]: " + text
		}
	}

	return text
}

// mediaPathRe matches "image_path: /path" or "audio_path: /path" lines in media summaries.
var mediaPathRe = regexp.MustCompile(`(?m)^(image_path|audio_path):\s*(.+)$`)

// generateMediaPreviews extracts media file paths from a media summary string,
// calls the previewer for each, and returns formatted preview tags.
// Returns empty string if no previews were generated or previewer is nil.
func (d *Dispatcher) generateMediaPreviews(mediaSummary string) string {
	if d.previewer == nil {
		return ""
	}

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

		mediaType := media.MediaTypeImage
		if pathType == "audio_path" {
			mediaType = media.MediaTypeAudio
		}

		description, err := d.previewer.Preview(d.ctx, filePath, mediaType)
		if err != nil {
			logger.Error("media preview failed",
				"file", filePath,
				"mediaType", pathType,
				"err", err,
			)
			previews = append(previews, media.FormatPreviewError(err, mediaType))
			continue
		}
		previews = append(previews, media.FormatPreviewTag(description, mediaType))
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

// persistChannelRouting writes channel.json to the session directory for
// channels that need routing metadata beyond what the session key provides
// (e.g., Discord DM needs "dm:{userID}" to create a DM channel on send,
// WeCom needs req_id to reply after service restart).
func persistChannelRouting(sessionsDir, sessionKey string, msg *channel.Message) {
	if msg == nil {
		return
	}

	var data map[string]any

	// Discord DM: persist reply_to for DM channel creation.
	chatType := strings.TrimSpace(msg.Metadata["chat_type"])
	if chatType == "dm" && strings.HasPrefix(msg.ChannelID, "discord:") {
		if userID := strings.TrimSpace(msg.UserID); userID != "" {
			data = map[string]any{
				"discord_dm": map[string]string{
					"channel":  "discord",
					"reply_to": "dm:" + userID,
					"user_id":  userID,
				},
			}
		}
	}

	// WeCom: persist req_id so heartbeat can reply after restart.
	if reqID := strings.TrimSpace(msg.Metadata[channel.MetaWeComReqID]); reqID != "" && strings.HasPrefix(sessionKey, "wecom:") {
		data = map[string]any{
			"wecom": map[string]string{
				"req_id": reqID,
			},
		}
	}

	if data == nil {
		return
	}

	sessionDir := session.SessionDir(sessionsDir, sessionKey)
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(sessionDir, 0755)
	_ = os.WriteFile(filepath.Join(sessionDir, "channel.json"), raw, 0644)
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
