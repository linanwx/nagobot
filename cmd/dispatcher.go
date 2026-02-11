package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		return "main"
	}

	if msg.ChannelID == "cli:local" || strings.HasPrefix(msg.ChannelID, "web:") {
		return "main"
	}

	if strings.HasPrefix(msg.ChannelID, "telegram:") {
		userID := strings.TrimSpace(msg.UserID)
		adminID := strings.TrimSpace(d.cfg.GetAdminUserID())
		if userID != "" && adminID != "" && userID == adminID {
			return "main"
		}
		if userID != "" {
			return "telegram:" + userID
		}
		return msg.ChannelID
	}

	if strings.HasPrefix(msg.ChannelID, "feishu:") {
		userID := strings.TrimSpace(msg.UserID)
		adminID := strings.TrimSpace(d.cfg.GetFeishuAdminOpenID())
		if userID != "" && adminID != "" && userID == adminID {
			return "main"
		}
		if userID != "" {
			return "feishu:" + userID
		}
		return msg.ChannelID
	}

	if strings.HasPrefix(msg.ChannelID, "cron:") {
		jobID := strings.TrimSpace(msg.Metadata["job_id"])
		if jobID == "" {
			jobID = "job"
		}
		timePart := time.Now().Local().Format("2006-01-02-15-04-05")
		suffix := thread.RandomHex(4)
		if suffix == "" {
			suffix = fmt.Sprintf("%d", time.Now().UnixNano())
		}
		return fmt.Sprintf("cron:%s:%s-%s", jobID, timePart, suffix)
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
		Label: "your response will be sent to the user via " + channelName,
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
	reportTo := strings.TrimSpace(msg.Metadata["report_to_session"])
	if reportTo == "" {
		reportTo = "main"
	}
	jobID := strings.TrimSpace(msg.Metadata["job_id"])

	if silent {
		return thread.Sink{Label: "cron silent, result will not be delivered"}
	}

	return thread.Sink{
		Label: "your response will be forwarded to session " + reportTo,
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
	if agentName == "" && msg.UserID != "" && d.cfg.Channels != nil {
		agentName = d.cfg.Channels.UserAgents[msg.UserID]
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

// preprocessMessage formats media metadata into the user message.
func (d *Dispatcher) preprocessMessage(msg *channel.Message) string {
	userMessage := msg.Text
	if mediaType := msg.Metadata["media_type"]; mediaType != "" {
		var mediaParts []string
		mediaParts = append(mediaParts, fmt.Sprintf("media_type: %s", mediaType))
		if fn := msg.Metadata["file_name"]; fn != "" {
			mediaParts = append(mediaParts, fmt.Sprintf("file_name: %s", fn))
		}
		if mime := msg.Metadata["mime_type"]; mime != "" {
			mediaParts = append(mediaParts, fmt.Sprintf("mime_type: %s", mime))
		}
		if url := msg.Metadata["file_url"]; url != "" {
			mediaParts = append(mediaParts, fmt.Sprintf("file_url: %s", url))
		}
		if dur := msg.Metadata["duration"]; dur != "" {
			mediaParts = append(mediaParts, fmt.Sprintf("duration: %ss", dur))
		}
		userMessage = fmt.Sprintf("[Media: %s]\n%s\n\n%s", mediaType, strings.Join(mediaParts, "\n"), msg.Text)
	}
	return userMessage
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
