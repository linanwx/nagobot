package channel

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
)

const (
	discordMessageBufferSize = 100
	DiscordMaxMessageLength  = 2000
)

// DiscordChannel implements the Channel interface for Discord.
type DiscordChannel struct {
	token         string
	allowedGuilds map[string]bool // guild ID allowlist, empty = allow all
	allowedUsers  map[string]bool // user ID allowlist, empty = allow all
	session       *discordgo.Session
	messages      chan *Message
}

// NewDiscordChannel creates a new Discord channel from config.
// Returns nil if no token is configured.
func NewDiscordChannel(cfg *config.Config) Channel {
	token := cfg.GetDiscordToken()
	if token == "" {
		logger.Warn("Discord token not configured, skipping Discord channel")
		return nil
	}

	allowedGuilds := make(map[string]bool)
	for _, id := range cfg.GetDiscordAllowedGuildIDs() {
		allowedGuilds[id] = true
	}
	allowedUsers := make(map[string]bool)
	for _, id := range cfg.GetDiscordAllowedUserIDs() {
		allowedUsers[id] = true
	}

	return &DiscordChannel{
		token:         token,
		allowedGuilds: allowedGuilds,
		allowedUsers:  allowedUsers,
		messages:      make(chan *Message, discordMessageBufferSize),
	}
}

func (d *DiscordChannel) Name() string { return "discord" }

func (d *DiscordChannel) Start(ctx context.Context) error {
	dg, err := discordgo.New("Bot " + d.token)
	if err != nil {
		return fmt.Errorf("discord session creation failed: %w", err)
	}

	dg.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentMessageContent

	dg.AddHandler(d.handleMessageCreate)

	if err := dg.Open(); err != nil {
		return fmt.Errorf("discord connection failed: %w", err)
	}
	d.session = dg
	logger.Info("discord bot connected", "username", dg.State.User.Username)

	go func() {
		<-ctx.Done()
		_ = d.Stop()
	}()

	logger.Info("discord channel started")
	return nil
}

func (d *DiscordChannel) Stop() error {
	if d.session != nil {
		_ = d.session.Close()
		d.session = nil
	}
	close(d.messages)
	logger.Info("discord channel stopped")
	return nil
}

func (d *DiscordChannel) Send(_ context.Context, resp *Response) error {
	if d.session == nil {
		return fmt.Errorf("discord session not started")
	}

	replyTo := resp.ReplyTo
	if strings.HasPrefix(replyTo, "dm:") {
		userID := strings.TrimPrefix(replyTo, "dm:")
		ch, err := d.session.UserChannelCreate(userID)
		if err != nil {
			return fmt.Errorf("discord DM channel creation failed: %w", err)
		}
		replyTo = ch.ID
	}

	chunks := SplitMessage(resp.Text, DiscordMaxMessageLength)
	for _, chunk := range chunks {
		if _, err := d.session.ChannelMessageSend(replyTo, chunk); err != nil {
			return fmt.Errorf("discord send error: %w", err)
		}
	}
	return nil
}

func (d *DiscordChannel) Messages() <-chan *Message {
	return d.messages
}

func (d *DiscordChannel) handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore self.
	if m.Author.ID == s.State.User.ID {
		return
	}
	// Ignore other bots.
	if m.Author.Bot {
		return
	}

	// Guild allowlist check.
	if m.GuildID != "" && len(d.allowedGuilds) > 0 && !d.allowedGuilds[m.GuildID] {
		return
	}
	// User allowlist check.
	if len(d.allowedUsers) > 0 && !d.allowedUsers[m.Author.ID] {
		return
	}

	text := m.Content

	// Resolve user mentions from <@userid> to @displayname.
	for _, u := range m.Mentions {
		name := u.GlobalName
		if name == "" {
			name = u.Username
		}
		text = strings.ReplaceAll(text, "<@"+u.ID+">", "@"+name)
		text = strings.ReplaceAll(text, "<@!"+u.ID+">", "@"+name)
	}

	metadata := map[string]string{
		"chat_id":  m.ChannelID,
		"guild_id": m.GuildID,
	}

	if m.GuildID != "" {
		metadata["chat_type"] = "group"
	} else {
		metadata["chat_type"] = "dm"
	}

	// Handle attachments.
	if len(m.Attachments) > 0 {
		var summaries []string
		for _, att := range m.Attachments {
			mediaType := "file"
			if strings.HasPrefix(att.ContentType, "image/") {
				mediaType = "image"
			} else if strings.HasPrefix(att.ContentType, "video/") {
				mediaType = "video"
			} else if strings.HasPrefix(att.ContentType, "audio/") {
				mediaType = "audio"
			}
			summaries = append(summaries, MediaSummary(mediaType,
				"file_name", att.Filename,
				"file_url", att.URL,
				"content_type", att.ContentType,
			))
		}
		metadata["media_summary"] = strings.Join(summaries, "\n\n")
		if text == "" {
			text = fmt.Sprintf("[%d attachment(s) received]", len(m.Attachments))
		}
	}

	if text == "" {
		return
	}

	// Acknowledge receipt with eyes reaction.
	_ = s.MessageReactionAdd(m.ChannelID, m.ID, "👀")

	// Resolve username: prefer display name, fallback to username.
	username := m.Author.GlobalName
	if username == "" {
		username = m.Author.Username
	}

	msg := &Message{
		ID:        m.ID,
		ChannelID: "discord:" + m.ChannelID,
		UserID:    m.Author.ID,
		Username:  username,
		Text:      text,
		Metadata:  metadata,
	}

	if m.MessageReference != nil {
		msg.ReplyTo = m.MessageReference.MessageID
	}

	select {
	case d.messages <- msg:
	default:
		logger.Warn("discord message buffer full, dropping message")
	}
}
