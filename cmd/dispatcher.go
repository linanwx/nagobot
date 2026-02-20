package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/thread"
)

// Dispatcher routes channel messages to threads. It is the bridge between
// the channel layer (pure I/O) and the thread layer (async execution).
type Dispatcher struct {
	channels *channel.Manager
	threads  *thread.Manager
	cfg      *config.Config
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
	d.channels.Each(func(ch channel.Channel) {
		go d.processChannel(ctx, ch)
	})
	<-ctx.Done()
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

func (d *Dispatcher) dispatch(_ context.Context, ch channel.Channel, msg *channel.Message) {
	logger.Debug("dispatching message",
		"channel", ch.Name(),
		"channelID", msg.ChannelID,
		"user", msg.Username,
		"text", truncate(msg.Text, 50),
	)

	sessionKey := d.route(msg)
	sink := d.buildSink(ch, msg)
	agentName, vars := d.resolveAgentName(msg)
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

// route determines the session key for a message.
func (d *Dispatcher) route(msg *channel.Message) string {
	if msg == nil {
		return "cli"
	}

	if msg.ChannelID == "cli:local" || strings.HasPrefix(msg.ChannelID, "web:") {
		return "cli"
	}

	if strings.HasPrefix(msg.ChannelID, "telegram:") {
		chatType := strings.TrimSpace(msg.Metadata["chat_type"])
		if chatType == "group" || chatType == "supergroup" {
			return msg.ChannelID // shared session for group
		}
		userID := strings.TrimSpace(msg.UserID)
		if userID != "" {
			return "telegram:" + userID
		}
		return msg.ChannelID
	}

	if strings.HasPrefix(msg.ChannelID, "feishu:") {
		chatType := strings.TrimSpace(msg.Metadata["chat_type"])
		if chatType == "group" {
			return msg.ChannelID // shared session for group
		}
		userID := strings.TrimSpace(msg.UserID)
		if userID != "" {
			return "feishu:" + userID
		}
		return msg.ChannelID
	}

	if strings.HasPrefix(msg.ChannelID, "discord:") {
		chatType := strings.TrimSpace(msg.Metadata["chat_type"])
		if chatType == "group" {
			return msg.ChannelID // shared session for guild channel
		}
		userID := strings.TrimSpace(msg.UserID)
		if userID != "" {
			return "discord:" + userID
		}
		return msg.ChannelID
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
		Idempotent: true,
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
			wakeMsg := fmt.Sprintf(
				"[Cron job completed]\n- id: %s\n- result:\n%s",
				jobID,
				strings.TrimSpace(response),
			)
			d.threads.Wake(reportTo, &thread.WakeMessage{
				Source:  "cron_finished",
				Message: wakeMsg,
			})
			return nil
		},
	}
}

// resolveAgentName returns the agent name and vars for a message.
// Empty name means use the default (soul) agent.
func (d *Dispatcher) resolveAgentName(msg *channel.Message) (string, map[string]string) {
	if msg == nil {
		return "", nil
	}

	agentName := strings.TrimSpace(msg.Metadata["agent"])
	if agentName == "" && d.cfg.Channels != nil {
		if msg.UserID != "" {
			agentName = d.cfg.Channels.UserAgents[msg.UserID]
		}
		if agentName == "" {
			if chatID := strings.TrimSpace(msg.Metadata["chat_id"]); chatID != "" {
				agentName = d.cfg.Channels.UserAgents[chatID]
			}
		}
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

// wakeSource returns the wake source string for a channel.
func (d *Dispatcher) wakeSource(ch channel.Channel) string {
	return ch.Name() // "telegram", "cli", "web", "cron", etc.
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
