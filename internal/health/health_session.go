package health

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/linanwx/nagobot/session"
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

	s, err := session.ReadFile(path)
	if err != nil {
		info.ParseError = err.Error()
		return info
	}

	info.MessagesCount = len(s.Messages)
	if !s.UpdatedAt.IsZero() {
		info.UpdatedAt = s.UpdatedAt.Format(time.RFC3339)
	}
	return info
}

func inspectSessionsRoot(ctx context.Context, root string) *SessionsInfo {
	info := &SessionsInfo{Root: root}

	stat, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			info.Exists = false
			return info
		}
		info.ScanError = err.Error()
		return info
	}
	if !stat.IsDir() {
		info.ScanError = "sessions root is not a directory"
		return info
	}
	info.Exists = true

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			if info.ScanError == "" {
				info.ScanError = walkErr.Error()
			}
			return nil
		}
		if d.IsDir() || d.Name() != session.SessionFileName {
			return nil
		}

		info.FilesCount++
		if _, readErr := session.ReadFile(path); readErr != nil {
			info.InvalidCount++
			info.InvalidFiles = append(info.InvalidFiles, SessionFileError{
				Path:       path,
				ParseError: readErr.Error(),
			})
			return nil
		}

		info.ValidCount++
		return nil
	})
	if walkErr != nil && info.ScanError == "" {
		info.ScanError = walkErr.Error()
	}

	return info
}

