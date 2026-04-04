package channel

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// initMediaDir creates and returns the media directory path for a config.
// Returns empty string if workspace is unavailable or mkdir fails.
func initMediaDir(cfg interface{ WorkspacePath() (string, error) }) string {
	ws, err := cfg.WorkspacePath()
	if err != nil {
		return ""
	}
	dir := filepath.Join(ws, "media")
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.Warn("failed to create media directory", "dir", dir, "err", err)
		return ""
	}
	return dir
}

// downloadMedia downloads a URL to mediaDir, returning the absolute local path.
// Returns empty string on error (caller should fall back to URL).
func downloadMedia(mediaDir, url string) string {
	if mediaDir == "" || url == "" {
		return ""
	}

	resp, err := http.Get(url)
	if err != nil {
		logger.Warn("failed to download media", "url", url, "err", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Warn("media download returned non-200", "url", url, "status", resp.StatusCode)
		return ""
	}

	// Detect extension: try URL path first, then Content-Type, then fallback.
	ext := extensionFromURL(url)
	if ext == "" {
		ext = extensionFromContentType(resp.Header.Get("Content-Type"))
	}
	if ext == "" {
		ext = ".dat"
	}

	// Choose filename prefix based on content type.
	prefix := "media"
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "image/"):
		prefix = "img"
	case strings.HasPrefix(ct, "audio/"):
		prefix = "audio"
	case strings.HasPrefix(ct, "video/"):
		prefix = "video"
	case ct == "application/pdf":
		prefix = "pdf"
	}

	buf := make([]byte, 4)
	rand.Read(buf)
	fileName := fmt.Sprintf("%s-%s-%s%s", prefix, time.Now().Format("20060102-150405"), hex.EncodeToString(buf), ext)
	filePath := filepath.Join(mediaDir, fileName)

	f, err := os.Create(filePath)
	if err != nil {
		logger.Warn("failed to create media file", "path", filePath, "err", err)
		return ""
	}
	defer f.Close()

	const maxMediaSize = 20 << 20 // 20 MB
	if _, err := io.Copy(f, io.LimitReader(resp.Body, maxMediaSize)); err != nil {
		logger.Warn("failed to write media file", "path", filePath, "err", err)
		os.Remove(filePath)
		return ""
	}

	return filePath
}

func extensionFromURL(url string) string {
	// Strip query string before checking extension.
	if idx := strings.IndexByte(url, '?'); idx >= 0 {
		url = url[:idx]
	}
	ext := strings.ToLower(filepath.Ext(url))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return ext
	case ".ogg", ".oga", ".mp3", ".wav", ".m4a", ".flac", ".aac", ".opus":
		return ext
	}
	return ""
}

func extensionFromContentType(ct string) string {
	switch {
	// Image types.
	case strings.HasPrefix(ct, "image/jpeg"):
		return ".jpg"
	case strings.HasPrefix(ct, "image/png"):
		return ".png"
	case strings.HasPrefix(ct, "image/gif"):
		return ".gif"
	case strings.HasPrefix(ct, "image/webp"):
		return ".webp"
	// Audio types.
	case strings.HasPrefix(ct, "audio/ogg"):
		return ".ogg"
	case strings.HasPrefix(ct, "audio/mpeg"):
		return ".mp3"
	case strings.HasPrefix(ct, "audio/mp4"), strings.HasPrefix(ct, "audio/m4a"):
		return ".m4a"
	case strings.HasPrefix(ct, "audio/wav"), strings.HasPrefix(ct, "audio/x-wav"):
		return ".wav"
	case strings.HasPrefix(ct, "audio/flac"):
		return ".flac"
	case strings.HasPrefix(ct, "audio/aac"):
		return ".aac"
	}
	return ""
}
