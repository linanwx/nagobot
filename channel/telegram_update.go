package channel

import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/linanwx/nagobot/logger"
)

// handleUpdate is the default handler for incoming Telegram updates.
func (t *TelegramChannel) handleUpdate(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	msg := update.Message
	chat := msg.Chat
	from := msg.From

	fromID := int64(0)
	username := ""
	firstName := ""
	lastName := ""
	if from != nil {
		fromID = from.ID
		username = from.Username
		firstName = from.FirstName
		lastName = from.LastName
	}

	t.mu.RLock()
	allowed := t.allowedIDs
	t.mu.RUnlock()
	if len(allowed) > 0 {
		if !allowed[chat.ID] && !allowed[fromID] {
			logger.Warn("telegram message from unauthorized user",
				"userID", fromID,
				"chatID", chat.ID,
				"username", username,
			)
			return
		}
	}

	// Determine text and media metadata
	text := msg.Text
	metadata := map[string]string{
		"chat_id":    strconv.FormatInt(chat.ID, 10),
		"chat_type":  string(chat.Type),
		"first_name": firstName,
		"last_name":  lastName,
	}

	switch {
	case len(msg.Photo) > 0:
		photo := msg.Photo[len(msg.Photo)-1]
		fileURL := t.getFileURL(ctx, b, photo.FileID)
		if localPath := downloadMedia(t.mediaDir, fileURL); localPath != "" {
			metadata["media_summary"] = MediaSummary("photo", "image_path", localPath)
		} else {
			metadata["media_summary"] = MediaSummary("photo", "file_url", fileURL)
		}
		if text == "" {
			text = msg.Caption
		}
		if text == "" {
			text = "[Photo received]"
		}
	case msg.Animation != nil:
		metadata["media_summary"] = MediaSummary("animation",
			"file_name", msg.Animation.FileName,
			"mime_type", msg.Animation.MimeType,
			"duration", fmtSeconds(msg.Animation.Duration),
			"file_url", t.getFileURL(ctx, b, msg.Animation.FileID))
		if text == "" {
			text = msg.Caption
		}
		if text == "" {
			text = "[GIF received]"
		}
	case msg.Document != nil:
		metadata["media_summary"] = MediaSummary("document",
			"file_name", msg.Document.FileName,
			"mime_type", msg.Document.MimeType,
			"file_url", t.getFileURL(ctx, b, msg.Document.FileID))
		if text == "" {
			text = msg.Caption
		}
		if text == "" {
			if msg.Document.FileName != "" {
				text = fmt.Sprintf("[Document: %s]", msg.Document.FileName)
			} else {
				text = "[Document received]"
			}
		}
	case msg.Voice != nil:
		metadata["media_summary"] = MediaSummary("voice",
			"duration", fmtSeconds(msg.Voice.Duration),
			"file_url", t.getFileURL(ctx, b, msg.Voice.FileID))
		if text == "" {
			text = msg.Caption
		}
		if text == "" {
			text = "[Voice message received]"
		}
	case msg.Video != nil:
		metadata["media_summary"] = MediaSummary("video",
			"file_name", msg.Video.FileName,
			"mime_type", msg.Video.MimeType,
			"duration", fmtSeconds(msg.Video.Duration),
			"file_url", t.getFileURL(ctx, b, msg.Video.FileID))
		if text == "" {
			text = msg.Caption
		}
		if text == "" {
			text = "[Video received]"
		}
	case msg.VideoNote != nil:
		metadata["media_summary"] = MediaSummary("video_note",
			"duration", fmtSeconds(msg.VideoNote.Duration),
			"file_url", t.getFileURL(ctx, b, msg.VideoNote.FileID))
		if text == "" {
			text = "[Video note received]"
		}
	case msg.Audio != nil:
		metadata["media_summary"] = MediaSummary("audio",
			"file_name", msg.Audio.FileName,
			"mime_type", msg.Audio.MimeType,
			"duration", fmtSeconds(msg.Audio.Duration),
			"file_url", t.getFileURL(ctx, b, msg.Audio.FileID))
		if text == "" {
			text = msg.Caption
		}
		if text == "" {
			text = "[Audio received]"
		}
	case msg.Sticker != nil:
		metadata["media_summary"] = MediaSummary("sticker",
			"emoji", msg.Sticker.Emoji,
			"sticker_set", msg.Sticker.SetName,
			"file_url", t.getFileURL(ctx, b, msg.Sticker.FileID))
		if text == "" {
			text = "[Sticker received]"
		}
	}

	// Skip empty messages (no text and no media)
	if text == "" {
		return
	}

	// React with eyes emoji to acknowledge receipt (fire-and-forget)
	_, _ = b.SetMessageReaction(ctx, &bot.SetMessageReactionParams{
		ChatID:    chat.ID,
		MessageID: msg.ID,
		Reaction: []models.ReactionType{
			{
				Type: models.ReactionTypeTypeEmoji,
				ReactionTypeEmoji: &models.ReactionTypeEmoji{
					Type:  models.ReactionTypeTypeEmoji,
					Emoji: "\U0001F440",
				},
			},
		},
	})

	channelMsg := &Message{
		ID:        strconv.Itoa(msg.ID),
		ChannelID: fmt.Sprintf("telegram:%d", chat.ID),
		UserID:    strconv.FormatInt(fromID, 10),
		Username:  username,
		Text:      text,
		Metadata:  metadata,
	}

	if msg.ReplyToMessage != nil {
		channelMsg.ReplyTo = strconv.Itoa(msg.ReplyToMessage.ID)
		if rc := telegramReplyContext(msg.ReplyToMessage); rc != "" {
			metadata["reply_context"] = rc
		}
	}

	select {
	case t.messages <- channelMsg:
	case <-t.done:
	default:
		logger.Warn("telegram message buffer full, dropping message")
	}
}

// telegramReplyContext builds a reply context string from a replied-to message.
func telegramReplyContext(m *models.Message) string {
	text := m.Text
	if text == "" {
		text = m.Caption
	}
	if text == "" {
		// Fallback for media-only messages (sticker, photo without caption, voice, etc.)
		switch {
		case m.Sticker != nil:
			text = "[Sticker" + ifNotEmpty(" ", m.Sticker.Emoji) + "]"
		case len(m.Photo) > 0:
			text = "[Photo]"
		case m.Voice != nil:
			text = "[Voice message]"
		case m.Video != nil:
			text = "[Video]"
		case m.Audio != nil:
			text = "[Audio]"
		case m.Document != nil:
			text = "[Document" + ifNotEmpty(": ", m.Document.FileName) + "]"
		case m.Animation != nil:
			text = "[GIF]"
		case m.VideoNote != nil:
			text = "[Video note]"
		default:
			return ""
		}
	}
	author := ""
	if m.From != nil {
		author = m.From.FirstName
		if m.From.LastName != "" {
			author += " " + m.From.LastName
		}
		if author == "" {
			author = m.From.Username
		}
	}
	if author == "" {
		author = "unknown"
	}
	return "[Reply to " + author + "]: " + text
}

// ifNotEmpty returns prefix+s when s is non-empty, otherwise "".
func ifNotEmpty(prefix, s string) string {
	if s != "" {
		return prefix + s
	}
	return ""
}

// getFileURL retrieves the download URL for a Telegram file.
func (t *TelegramChannel) getFileURL(ctx context.Context, b *bot.Bot, fileID string) string {
	file, err := b.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
	if err != nil {
		logger.Warn("failed to get telegram file URL", "fileID", fileID, "err", err)
		return ""
	}
	return b.FileDownloadLink(file)
}
