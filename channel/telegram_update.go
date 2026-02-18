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

	if len(t.allowedIDs) > 0 {
		if !t.allowedIDs[chat.ID] && !t.allowedIDs[fromID] {
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
		metadata["media_summary"] = MediaSummary("photo",
			"file_url", t.getFileURL(ctx, b, photo.FileID))
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
	}

	select {
	case t.messages <- channelMsg:
	default:
		logger.Warn("telegram message buffer full, dropping message")
	}
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
