package channel

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/websocket"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
)

const (
	discordMessageBufferSize = 100
	DiscordMaxMessageLength  = 2000

	// discordOpenTimeout bounds a single Open() attempt. discordgo's Open()
	// performs two deadline-less blocking ReadMessage calls (waiting for the
	// Op 10 Hello and the READY/RESUMED packets) while holding the session
	// mutex; if the server accepts the websocket but never speaks again,
	// Open() blocks forever and every other session call deadlocks on the
	// mutex. An Open() that outlives the timeout is abandoned.
	discordOpenTimeout = 20 * time.Second
	// The supervisor checks DataReady every discordWatchInterval and rebuilds
	// the session after discordNotReadyLimit consecutive not-ready checks.
	// ~2 minutes lets discordgo's own reconnect loop survive transient drops
	// (its backoff passes 64s within that window) before we intervene.
	discordWatchInterval = 30 * time.Second
	discordNotReadyLimit = 4
	// discordStartAttempts bounds the initial connect attempts inside Start()
	// so the hot-reload loop that calls us never blocks for long.
	discordStartAttempts = 3
)

// DiscordChannel implements the Channel interface for Discord.
type DiscordChannel struct {
	token         string
	allowedGuilds map[string]bool // guild ID allowlist, empty = allow all
	allowedUsers  map[string]bool // user ID allowlist, empty = allow all
	mediaDir      string          // local directory for downloaded media files
	session       *discordgo.Session
	sessionMu     sync.Mutex // guards session; the supervisor swaps it while other goroutines read it
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

func (d *DiscordChannel) currentSession() *discordgo.Session {
	d.sessionMu.Lock()
	defer d.sessionMu.Unlock()
	return d.session
}

func (d *DiscordChannel) setSession(s *discordgo.Session) {
	d.sessionMu.Lock()
	defer d.sessionMu.Unlock()
	d.session = s
}

// newSession builds a fresh discordgo session. Each rebuild gets its own
// session because a session whose Open() hung keeps its mutex locked forever —
// it cannot be reused, only abandoned.
func (d *DiscordChannel) newSession() (*discordgo.Session, error) {
	dg, err := discordgo.New("Bot " + d.token)
	if err != nil {
		return nil, err
	}

	dg.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentMessageContent

	// An abandoned session's own reconnect loop may revive it later and
	// reconnect to the gateway; drop its events so only the current session's
	// messages are processed.
	dg.AddHandler(d.sessionHandler())

	// Explicit handshake timeout (gorilla's default is 45s): fail fast so the
	// bounded Open() below spends its budget on the reads that have no deadline.
	dg.Dialer = &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 15 * time.Second,
	}
	return dg, nil
}

// sessionHandler returns the MessageCreate handler bound to this channel. It
// drops events from sessions other than the current one: an abandoned session
// (its Open() hung, or its reconnect loop revived after a rebuild) may still
// deliver gateway events, and processing them would duplicate messages.
func (d *DiscordChannel) sessionHandler() func(*discordgo.Session, *discordgo.MessageCreate) {
	return func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if s != d.currentSession() {
			return
		}
		d.handleMessageCreate(s, m)
	}
}

// connect runs Open() under a hard timeout. On timeout the session is
// abandoned: the goroutine stuck inside Open() leaks (unavoidable — it holds
// the session mutex), and the caller retries with a fresh session.
func discordConnect(dg *discordgo.Session, timeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() { errCh <- dg.Open() }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(timeout):
		go func() {
			if err := <-errCh; err == nil {
				// Open() eventually succeeded after we gave up on it.
				_ = dg.Close()
			}
		}()
		return fmt.Errorf("discord Open timed out after %s (session abandoned)", timeout)
	}
}

func (d *DiscordChannel) Start(ctx context.Context) error {
	var lastErr error
	for attempt := 1; attempt <= discordStartAttempts; attempt++ {
		dg, err := d.newSession()
		if err != nil {
			return fmt.Errorf("discord session creation failed: %w", err)
		}
		if err := discordConnect(dg, discordOpenTimeout); err != nil {
			lastErr = fmt.Errorf("discord connection failed: %w", err)
			logger.Warn("discord connect attempt failed", "attempt", attempt, "err", err)
			continue
		}
		d.setSession(dg)
		logger.Info("discord bot connected", "username", dg.State.User.Username)

		go d.supervise(ctx)
		go func() {
			<-ctx.Done()
			_ = d.Stop()
		}()

		logger.Info("discord channel started")
		return nil
	}
	return lastErr
}

// supervise watches the session and rebuilds it when it stays not-ready past
// the tolerance window. This covers both a hung Open() (deadlocked session,
// discordgo's reconnect can never run again) and any other silent death where
// discordgo's own infinite reconnect loop has given up or was never started.
func (d *DiscordChannel) supervise(ctx context.Context) {
	ticker := time.NewTicker(discordWatchInterval)
	defer ticker.Stop()
	notReady := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.done:
			return
		case <-ticker.C:
			if dg := d.currentSession(); dg != nil && dg.DataReady {
				notReady = 0
				continue
			}
			notReady++
			if notReady < discordNotReadyLimit {
				continue
			}
			notReady = 0
			logger.Warn("discord session not ready for too long, rebuilding",
				"checks", discordNotReadyLimit, "interval", discordWatchInterval.String())
			d.rebuild()
		}
	}
}

// rebuild swaps the current session for a freshly connected one. The old
// session is only abandoned, never synchronously closed — its Close() can
// block forever on the mutex a hung Open() still holds.
func (d *DiscordChannel) rebuild() {
	if old := d.currentSession(); old != nil {
		d.setSession(nil)
		go func() { _ = old.Close() }() // best effort; may leak if hung
	}
	dg, err := d.newSession()
	if err != nil {
		logger.Warn("discord session rebuild: creation failed", "err", err)
		return
	}
	if err := discordConnect(dg, discordOpenTimeout); err != nil {
		logger.Warn("discord session rebuild: connect failed", "err", err)
		go func() { _ = dg.Close() }()
		return
	}
	d.setSession(dg)
	logger.Info("discord bot reconnected", "username", dg.State.User.Username)
}

func (d *DiscordChannel) Stop() error {
	d.stopOnce.Do(func() {
		close(d.done)
		// Close in the background: on a hung session it blocks forever, and
		// StopAll must not stall behind it.
		if ses := d.currentSession(); ses != nil {
			go func() { _ = ses.Close() }()
		}
		d.setSession(nil)
		close(d.messages)
		logger.Info("discord channel stopped")
	})
	return nil
}

func (d *DiscordChannel) Send(_ context.Context, resp *Response) error {
	ses := d.currentSession()
	if ses == nil {
		return fmt.Errorf("discord session not started")
	}
	replyTo, err := d.resolveTarget(ses, resp.ReplyTo)
	if err != nil {
		return err
	}

	text := convertTablesToLists(resp.Text)
	chunks := SplitMessage(text, DiscordMaxMessageLength)
	for _, chunk := range chunks {
		if _, err := ses.ChannelMessageSend(replyTo, chunk); err != nil {
			return fmt.Errorf("discord send error: %w", err)
		}
	}
	return nil
}

// resolveTarget resolves a "dm:{userID}" target to a real DM channel ID.
// Plain channel IDs pass through unchanged.
func (d *DiscordChannel) resolveTarget(ses *discordgo.Session, target string) (string, error) {
	userID, ok := strings.CutPrefix(target, "dm:")
	if !ok {
		return target, nil
	}
	ch, err := ses.UserChannelCreate(userID)
	if err != nil {
		return "", fmt.Errorf("discord DM channel creation failed: %w", err)
	}
	return ch.ID, nil
}

// SendImage uploads ref as a Discord attachment. Target convention matches Send.
func (d *DiscordChannel) SendImage(_ context.Context, replyTo string, ref ImageRef) error {
	ses := d.currentSession()
	if ses == nil {
		return fmt.Errorf("discord session not started")
	}
	target, err := d.resolveTarget(ses, replyTo)
	if err != nil {
		return err
	}
	f, err := os.Open(ref.Path)
	if err != nil {
		return fmt.Errorf("open image %s: %w", ref.Path, err)
	}
	defer f.Close()

	_, err = ses.ChannelMessageSendComplex(target, &discordgo.MessageSend{
		Files: []*discordgo.File{{
			Name:        filepath.Base(ref.Path),
			ContentType: ref.Mime,
			Reader:      f,
		}},
	})
	if err != nil {
		return fmt.Errorf("discord image send: %w", err)
	}
	return nil
}

// Compile-time check: DiscordChannel implements ImageSender.
var _ ImageSender = (*DiscordChannel)(nil)

// SendDoc uploads ref as a Discord file attachment. Target convention matches
// Send. Discord enforces its own size limit (25 MB without Nitro boost) and
// rejects oversized uploads — the error is returned to the caller for logging.
func (d *DiscordChannel) SendDoc(_ context.Context, replyTo string, ref DocRef) error {
	ses := d.currentSession()
	if ses == nil {
		return fmt.Errorf("discord session not started")
	}
	target, err := d.resolveTarget(ses, replyTo)
	if err != nil {
		return err
	}
	f, err := os.Open(ref.Path)
	if err != nil {
		return fmt.Errorf("open doc %s: %w", ref.Path, err)
	}
	defer f.Close()

	name := ref.Name
	if name == "" {
		name = filepath.Base(ref.Path)
	}

	_, err = ses.ChannelMessageSendComplex(target, &discordgo.MessageSend{
		Files: []*discordgo.File{{
			Name:        name,
			ContentType: ref.Mime,
			Reader:      f,
		}},
	})
	if err != nil {
		return fmt.Errorf("discord doc send: %w", err)
	}
	return nil
}

// Compile-time check: DiscordChannel implements DocSender.
var _ DocSender = (*DiscordChannel)(nil)

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

	rowLabelCol := headers[0] == ""
	var out []string
	for i, row := range dataRows {
		if rowLabelCol && len(row) > 0 && row[0] != "" {
			out = append(out, fmt.Sprintf("**%d. %s**", i+1, row[0]))
		} else {
			out = append(out, fmt.Sprintf("**%d.**", i+1))
		}
		startCol := 0
		if rowLabelCol {
			startCol = 1
		}
		for j := startCol; j < numCols && j < len(row); j++ {
			h := headers[j]
			if h == "" {
				out = append(out, fmt.Sprintf("• %s", row[j]))
			} else {
				out = append(out, fmt.Sprintf("• **%s**: %s", h, row[j]))
			}
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

// isSeparatorRow checks if all cells look like |---| (must contain at least one dash).
func isSeparatorRow(cells []string) bool {
	hasDash := false
	for _, c := range cells {
		cleaned := strings.Trim(c, "-: ")
		if cleaned != "" {
			return false
		}
		if strings.Contains(c, "-") {
			hasDash = true
		}
	}
	return hasDash
}

func (d *DiscordChannel) Messages() <-chan *Message {
	return d.messages
}

// ReactTo adds an emoji reaction to a message (accumulative).
func (d *DiscordChannel) ReactTo(_ context.Context, chatID, msgID, emoji string) error {
	ses := d.currentSession()
	if ses == nil {
		return nil
	}
	_ = ses.MessageReactionAdd(chatID, msgID, emoji)
	return nil
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

	// Enrich metadata with thread / forum-post context when the message arrives
	// in a thread. Silently no-ops for regular channels and on API errors.
	for k, v := range threadContext(s, m.ChannelID) {
		metadata[k] = v
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
			// Download images, audio, and PDFs to local media directory so LLM can read them directly.
			if att.ContentType == "application/pdf" {
				mediaType = "document"
			}
			if mediaType == "image" || mediaType == "audio" || mediaType == "document" {
				if localPath := downloadMedia(d.mediaDir, att.URL); localPath != "" {
					pathKey := "image_path"
					if mediaType == "audio" {
						pathKey = "audio_path"
					} else if mediaType == "document" {
						pathKey = "document_path"
					}
					summaries = append(summaries, MediaSummary(mediaType,
						"file_name", att.Filename,
						pathKey, localPath,
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
		if ref := m.ReferencedMessage; ref != nil && ref.Content != "" {
			author := "unknown"
			if ref.Author != nil {
				author = ref.Author.GlobalName
				if author == "" {
					author = ref.Author.Username
				}
			}
			metadata["reply_context"] = "[Reply to " + author + "]: " + ref.Content
		}
	}

	select {
	case d.messages <- msg:
	case <-d.done:
	default:
		logger.Warn("discord message buffer full, dropping message")
	}
}

// threadContext fetches the current channel (and its parent if the channel is a
// thread) and builds metadata describing thread / forum-post context.
// Returns an empty map for non-thread channels or when API calls fail.
func threadContext(s *discordgo.Session, channelID string) map[string]string {
	empty := map[string]string{}
	if s == nil || channelID == "" {
		return empty
	}

	ch := lookupChannel(s, channelID)
	if ch == nil || !ch.IsThread() {
		return empty
	}

	var parent *discordgo.Channel
	if ch.ParentID != "" {
		parent = lookupChannel(s, ch.ParentID)
	}
	return buildThreadContext(ch, parent)
}

// lookupChannel returns a channel, consulting the session state cache first and
// falling back to an API call. Returns nil on error.
func lookupChannel(s *discordgo.Session, id string) *discordgo.Channel {
	if s == nil || id == "" {
		return nil
	}
	if s.State != nil {
		if ch, err := s.State.Channel(id); err == nil && ch != nil {
			return ch
		}
	}
	ch, err := s.Channel(id)
	if err != nil || ch == nil {
		if err != nil {
			logger.Warn("discord channel lookup failed", "id", id, "err", err)
		}
		return nil
	}
	return ch
}

// buildThreadContext turns a thread + its parent channel into metadata fields.
// Pure function: takes already-fetched Channel objects so it can be unit tested
// without a live discordgo Session. Returns empty map for non-thread inputs.
func buildThreadContext(thread, parent *discordgo.Channel) map[string]string {
	out := map[string]string{}
	if thread == nil || !thread.IsThread() {
		return out
	}

	if thread.Name != "" {
		out["thread_name"] = thread.Name
	}
	out["thread_type"] = "thread"

	if parent == nil || parent.Type != discordgo.ChannelTypeGuildForum {
		return out
	}

	out["thread_type"] = "forum_post"
	if parent.Name != "" {
		out["forum_name"] = parent.Name
	}

	if len(thread.AppliedTags) == 0 || len(parent.AvailableTags) == 0 {
		return out
	}

	tagMap := make(map[string]string, len(parent.AvailableTags))
	for _, t := range parent.AvailableTags {
		tagMap[t.ID] = t.Name
	}
	var names []string
	for _, id := range thread.AppliedTags {
		if name, ok := tagMap[id]; ok && name != "" {
			names = append(names, name)
		}
	}
	if len(names) > 0 {
		out["applied_tags"] = strings.Join(names, ", ")
	}
	return out
}
