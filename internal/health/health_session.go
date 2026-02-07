package health

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
)

func inspectSessionFile(path string) *SessionInfo {
	info := &SessionInfo{Path: path}

	stat, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			info.Exists = false
			return info
		}
		info.ParseError = err.Error()
		return info
	}

	info.Exists = true
	info.FileSizeBytes = stat.Size()
	info.UpdatedAt = stat.ModTime().Format(time.RFC3339)

	data, err := os.ReadFile(path)
	if err != nil {
		info.ParseError = err.Error()
		return info
	}

	var payload struct {
		Messages  []json.RawMessage `json:"messages"`
		UpdatedAt string            `json:"updated_at"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		info.ParseError = err.Error()
		return info
	}

	info.MessagesCount = len(payload.Messages)
	if strings.TrimSpace(payload.UpdatedAt) != "" {
		info.UpdatedAt = strings.TrimSpace(payload.UpdatedAt)
	}
	return info
}
