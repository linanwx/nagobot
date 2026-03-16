package channel

import (
	"context"
	"fmt"
	"strings"
	"sync"

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
	mediaDir      string // local directory for downloaded media files
	session       *discordgo.Session
	messages      chan *Message
	done          chan struct{}
	stopOnce      sync.Once
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

	mediaDir := initMediaDir(cfg)

	return &DiscordChannel{
		token:         token,
		allowedGuilds: allowedGuilds,
		allowedUsers:  allowedUsers,
		mediaDir:      mediaDir,
		messages:      make(chan *Message, discordMessageBufferSize),
		done:          make(chan struct{}),
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
	d.stopOnce.Do(func() {
		close(d.done)
		if d.session != nil {
			_ = d.session.Close()
			d.session = nil
		}
		close(d.messages)
		logger.Info("discord channel stopped")
	})
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

	text := convertTablesToLists(resp.Text)
	chunks := SplitMessage(text, DiscordMaxMessageLength)
	for _, chunk := range chunks {
		if _, err := d.session.ChannelMessageSend(replyTo, chunk); err != nil {
			return fmt.Errorf("discord send error: %w", err)
		}
	}
	return nil
}

// convertTablesToLists converts Markdown tables into numbered list format
// because Discord's table rendering is poor (misaligned, broken on mobile).
func convertTablesToLists(text string) string {
	lines := strings.Split(text, "\n")
	var result []string
	inCodeBlock := false

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Track code blocks — don't touch tables inside them.
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			result = append(result, line)
			i++
			continue
		}
		if inCodeBlock {
			result = append(result, line)
			i++
			continue
		}

		// Detect table block: consecutive lines starting with |
		if strings.HasPrefix(trimmed, "|") {
			tableStart := i
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				i++
			}
			tableLines := lines[tableStart:i]
			result = append(result, renderTableAsList(tableLines)...)
			continue
		}

		result = append(result, line)
		i++
	}

	return strings.Join(result, "\n")
}

// renderTableAsList converts parsed table lines into a numbered list.
func renderTableAsList(tableLines []string) []string {
	var headers []string
	var dataRows [][]string

	for _, line := range tableLines {
		cells := parseTableRow(line)
		if cells == nil {
			continue
		}
		// Skip separator rows (|---|---|)
		if isSeparatorRow(cells) {
			continue
		}
		if headers == nil {
			headers = cells
		} else {
			dataRows = append(dataRows, cells)
		}
	}

	if headers == nil {
		return tableLines // can't parse, return as-is
	}

	// Normalize column count.
	numCols := len(headers)
	for _, row := range dataRows {
		if len(row) > numCols {
			numCols = len(row)
		}
	}
	for len(headers) < numCols {
		headers = append(headers, "")
	}

	var out []string
	for i, row := range dataRows {
		out = append(out, fmt.Sprintf("**%d.**", i+1))
		for j := 0; j < numCols && j < len(row); j++ {
			h := headers[j]
			if h == "" {
				h = fmt.Sprintf("Column %d", j+1)
			}
			out = append(out, fmt.Sprintf("• **%s**: %s", h, row[j]))
		}
		if i < len(dataRows)-1 {
			out = append(out, "")
		}
	}
	return out
}

// parseTableRow splits a |-delimited row into trimmed cell values.
func parseTableRow(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") {
		return nil
	}
	// Trim leading and trailing |
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

// isSeparatorRow checks if all cells look like |---|
func isSeparatorRow(cells []string) bool {
	for _, c := range cells {
		cleaned := strings.Trim(c, "-: ")
		if cleaned != "" {
			return false
		}
	}
	return true
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
			// Download images to local media directory so LLM can read them directly.
			if mediaType == "image" {
				if localPath := downloadMedia(d.mediaDir, att.URL); localPath != "" {
					summaries = append(summaries, MediaSummary(mediaType,
						"file_name", att.Filename,
						"image_path", localPath,
						"content_type", att.ContentType,
					))
					continue
				}
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
	case <-d.done:
	default:
		logger.Warn("discord message buffer full, dropping message")
	}
}
