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
	case ".pdf", ".docx", ".doc", ".xlsx", ".xls", ".pptx", ".ppt",
		".csv", ".tsv", ".txt", ".md", ".rtf",
		".json", ".xml", ".yaml", ".yml", ".html", ".htm",
		".zip", ".tar", ".gz", ".7z", ".rar":
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
	// Document types.
	case strings.HasPrefix(ct, "application/pdf"):
		return ".pdf"
	case strings.HasPrefix(ct, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"):
		return ".docx"
	case strings.HasPrefix(ct, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"):
		return ".xlsx"
	case strings.HasPrefix(ct, "application/vnd.openxmlformats-officedocument.presentationml.presentation"):
		return ".pptx"
	case strings.HasPrefix(ct, "application/msword"):
		return ".doc"
	case strings.HasPrefix(ct, "application/vnd.ms-excel"):
		return ".xls"
	case strings.HasPrefix(ct, "application/vnd.ms-powerpoint"):
		return ".ppt"
	case strings.HasPrefix(ct, "application/rtf"), strings.HasPrefix(ct, "text/rtf"):
		return ".rtf"
	case strings.HasPrefix(ct, "text/csv"):
		return ".csv"
	case strings.HasPrefix(ct, "text/tab-separated-values"):
		return ".tsv"
	case strings.HasPrefix(ct, "text/markdown"):
		return ".md"
	case strings.HasPrefix(ct, "text/html"):
		return ".html"
	case strings.HasPrefix(ct, "text/xml"), strings.HasPrefix(ct, "application/xml"):
		return ".xml"
	case strings.HasPrefix(ct, "application/json"):
		return ".json"
	case strings.HasPrefix(ct, "application/zip"):
		return ".zip"
	case strings.HasPrefix(ct, "application/x-7z-compressed"):
		return ".7z"
	case strings.HasPrefix(ct, "application/x-rar-compressed"), strings.HasPrefix(ct, "application/vnd.rar"):
		return ".rar"
	case strings.HasPrefix(ct, "application/gzip"), strings.HasPrefix(ct, "application/x-gzip"):
		return ".gz"
	case strings.HasPrefix(ct, "application/x-tar"):
		return ".tar"
	case strings.HasPrefix(ct, "text/plain"):
		return ".txt"
	}
	return ""
}
