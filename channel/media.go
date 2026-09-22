package channel

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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

// errUnsupportedFileType reports an upload whose type resolved to no allowed
// extension from either its filename or its content type. The web upload
// handler maps it to 415; anything else saveMediaFile returns is an I/O
// failure and maps to 4xx/5xx generically.
var errUnsupportedFileType = errors.New("unsupported file type")

// imageMediaExtensions / audioMediaExtensions back both the upload whitelist
// and the media_summary classification, so they are named sets rather than
// inline switch arms: webMediaSummary asks "is this an image/audio extension"
// while the whitelist asks "is this allowed at all", and the two must not
// drift apart.
var imageMediaExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".bmp": true,
}

var audioMediaExtensions = map[string]bool{
	".ogg": true, ".oga": true, ".opus": true, ".mp3": true,
	".wav": true, ".m4a": true, ".flac": true, ".aac": true,
}

// allowedMediaExtensions is the one whitelist of extensions a file may carry
// into {workspace}/media: images, audio, documents, plain text, code, and
// archives. Video is deliberately absent — no provider can consume it, so
// Telegram/Discord never download it either. Extending the set is the whole
// procedure for accepting a new type.
var allowedMediaExtensions = map[string]bool{}

func init() {
	for ext := range imageMediaExtensions {
		allowedMediaExtensions[ext] = true
	}
	for ext := range audioMediaExtensions {
		allowedMediaExtensions[ext] = true
	}
	for _, ext := range []string{
		// Documents.
		".pdf", ".docx", ".doc", ".xlsx", ".xls", ".pptx", ".ppt", ".rtf",
		// Plain text and data.
		".csv", ".tsv", ".txt", ".md", ".json", ".xml", ".yaml", ".yml",
		".html", ".htm",
		// Code and config.
		".py", ".go", ".js", ".mjs", ".ts", ".tsx", ".jsx", ".sh", ".bash",
		".zsh", ".rb", ".rs", ".java", ".c", ".h", ".cpp", ".hpp", ".cs",
		".php", ".sql", ".log", ".ini", ".toml", ".conf", ".cfg", ".env",
		// Archives.
		".zip", ".tar", ".gz", ".7z", ".rar",
	} {
		allowedMediaExtensions[ext] = true
	}
}

// saveMediaFile writes raw bytes from r into mediaDir under a generated name
// derived from contentType and filename (same naming scheme as downloadMedia),
// returning the basename. Used by channels that receive bytes directly (e.g.
// the web console's paste/upload) rather than a URL to fetch. Caps the read at
// maxMediaSize.
//
// The client-supplied filename is the primary source for the extension: a
// code file arrives as text/plain or application/octet-stream, and only its
// name says ".py". Content-Type is the fallback for pasted blobs whose
// generated name carries the right type anyway ("image.png"). A file with no
// allowed extension from either source is rejected — that check is the upload
// endpoint's whole type policy.
func saveMediaFile(mediaDir, filename, contentType string, r io.Reader) (string, error) {
	if mediaDir == "" {
		return "", fmt.Errorf("media directory unavailable")
	}
	ext := extensionFromFilename(filename)
	if ext == "" {
		ext = extensionFromContentType(contentType)
	}
	if ext == "" {
		return "", fmt.Errorf("%w: content type %q, filename %q", errUnsupportedFileType, contentType, filename)
	}

	// Prefix follows the resolved extension, not the content type: a PDF
	// arriving as octet-stream with a filename is still a pdf-* file.
	prefix := "media"
	switch {
	case imageMediaExtensions[ext]:
		prefix = "img"
	case audioMediaExtensions[ext]:
		prefix = "audio"
	case ext == ".pdf":
		prefix = "pdf"
	}

	buf := make([]byte, 4)
	rand.Read(buf)
	fileName := fmt.Sprintf("%s-%s-%s%s", prefix, time.Now().Format("20060102-150405"), hex.EncodeToString(buf), ext)
	filePath := filepath.Join(mediaDir, fileName)

	f, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("create media file: %w", err)
	}
	defer f.Close()

	const maxMediaSize = 20 << 20 // 20 MB
	if _, err := io.Copy(f, io.LimitReader(r, maxMediaSize)); err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("write media file: %w", err)
	}

	return fileName, nil
}

func extensionFromURL(url string) string {
	// Strip query string before checking extension.
	if idx := strings.IndexByte(url, '?'); idx >= 0 {
		url = url[:idx]
	}
	ext := strings.ToLower(filepath.Ext(url))
	if allowedMediaExtensions[ext] {
		return ext
	}
	return ""
}

// extensionFromFilename resolves an upload's original file name against the
// whitelist. Basename-cleaned so a path-shaped name resolves to its tail, the
// same rule the stored-name path applies.
func extensionFromFilename(name string) string {
	return extensionFromURL(filepath.Base(strings.TrimSpace(name)))
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
