package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
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

	return thread.Sink{
		Label:      "your response will be sent to the user via " + channelName,
		Chunkable: true,
		Send: func(ctx context.Context, response string) error {
			if strings.TrimSpace(response) == "" {
				return nil
			}
			return manager.SendTo(ctx, channelName, response, replyTo)
		},
	}
}

// buildCronSink creates a sink for cron jobs that wakes the creator thread
// with the result.
func (d *Dispatcher) buildCronSink(msg *channel.Message) thread.Sink {
	if msg == nil {
		return thread.Sink{}
	}

	silent := msg.Metadata["silent"] == "true"
	reportTo := strings.TrimSpace(msg.Metadata["wake_session"])
	if reportTo == "" {
		reportTo = "cli"
	}
	jobID := strings.TrimSpace(msg.Metadata["job_id"])

	if silent {
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

// preprocessMessage prepends media summary and sender name to the user message.
func (d *Dispatcher) preprocessMessage(msg *channel.Message) string {
	text := msg.Text
	if summary := msg.Metadata["media_summary"]; summary != "" {
		text = summary + "\n\n" + text
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

// wakeSource returns the wake source for a channel.
func (d *Dispatcher) wakeSource(ch channel.Channel) thread.WakeSource {
	return thread.WakeSource(ch.Name())
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
