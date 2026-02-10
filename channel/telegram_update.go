package channel

import (
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/linanwx/nagobot/logger"
)

// processUpdate handles a single update.
func (t *TelegramChannel) processUpdate(update tgbotapi.Update) {
	if update.Message == nil {
		return
	}

	msg := update.Message
	chat := msg.Chat
	if chat == nil {
		return
	}

	from := msg.From
	fromID := int64(0)
	username := ""
	firstName := ""
	lastName := ""
	if from != nil {
		fromID = from.ID
		username = from.UserName
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
		"chat_type":  chat.Type,
		"first_name": firstName,
		"last_name":  lastName,
	}

	switch {
	case len(msg.Photo) > 0:
		// Use the largest photo (last in the slice)
		photo := msg.Photo[len(msg.Photo)-1]
		metadata["media_type"] = "photo"
		metadata["file_id"] = photo.FileID
		if fileURL := t.getFileURL(photo.FileID); fileURL != "" {
			metadata["file_url"] = fileURL
		}
		if text == "" {
			text = msg.Caption
		}
		if text == "" {
			text = "[Photo received]"
		}
	case msg.Document != nil:
		metadata["media_type"] = "document"
		metadata["file_id"] = msg.Document.FileID
		metadata["file_name"] = msg.Document.FileName
		metadata["mime_type"] = msg.Document.MimeType
		if fileURL := t.getFileURL(msg.Document.FileID); fileURL != "" {
			metadata["file_url"] = fileURL
		}
		if text == "" {
			text = msg.Caption
		}
		if text == "" {
			text = fmt.Sprintf("[Document: %s]", msg.Document.FileName)
		}
	case msg.Voice != nil:
		metadata["media_type"] = "voice"
		metadata["file_id"] = msg.Voice.FileID
		metadata["duration"] = strconv.Itoa(msg.Voice.Duration)
		if fileURL := t.getFileURL(msg.Voice.FileID); fileURL != "" {
			metadata["file_url"] = fileURL
		}
		if text == "" {
			text = "[Voice message received]"
		}
	}

	// Skip empty messages (no text and no media)
	if text == "" {
		return
	}

	channelMsg := &Message{
		ID:        strconv.Itoa(msg.MessageID),
		ChannelID: fmt.Sprintf("telegram:%d", chat.ID),
		UserID:    strconv.FormatInt(fromID, 10),
		Username:  username,
		Text:      text,
		Metadata:  metadata,
	}

	if msg.ReplyToMessage != nil {
		channelMsg.ReplyTo = strconv.Itoa(msg.ReplyToMessage.MessageID)
	}

	select {
	case t.messages <- channelMsg:
	default:
		logger.Warn("telegram message buffer full, dropping message")
	}
}

// getFileURL retrieves the download URL for a Telegram file.
func (t *TelegramChannel) getFileURL(fileID string) string {
	if t.bot == nil {
		return ""
	}
	fileCfg := tgbotapi.FileConfig{FileID: fileID}
	file, err := t.bot.GetFile(fileCfg)
	if err != nil {
		logger.Warn("failed to get telegram file URL", "fileID", fileID, "err", err)
		return ""
	}
	return file.Link(t.token)
}

// SplitMessage splits a long message into chunks.
func SplitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		// Try to split at newline
		splitAt := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
			splitAt = idx + 1
		}

		chunks = append(chunks, text[:splitAt])
		text = text[splitAt:]
	}

	return chunks
}
