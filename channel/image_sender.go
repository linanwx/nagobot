package channel

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/linanwx/nagobot/logger"
)

// ImageRef is one validated image attachment ready for delivery.
// Path is absolute and the file is confirmed to exist with image/* mime.
type ImageRef struct {
	Path string
	Alt  string
	Mime string
}

// ImageSender is an optional capability on Channel implementations.
// Channels that implement this interface receive automatic image delivery
// when the response text contains Markdown image syntax.
type ImageSender interface {
	SendImage(ctx context.Context, replyTo string, ref ImageRef) error
}

// dispatchImageRefs parses text for Markdown image references, resolves
// each against workspace (if relative), validates it as an image file,
// and calls ch.SendImage for each surviving ref. Errors are logged but
// never returned — image delivery is a best-effort side-effect on top
// of the already-delivered text.
//
// If ch does not implement ImageSender, this is a no-op.
// If workspace is "", relative paths are skipped (logged at WARN).
func dispatchImageRefs(ctx context.Context, ch Channel, replyTo, text, workspace string) {
	sender, ok := ch.(ImageSender)
	if !ok {
		return
	}
	parsed := parseMarkdownImages(text)
	for _, p := range parsed {
		path := p.RawPath
		if !filepath.IsAbs(path) {
			if workspace == "" {
				logger.Warn("image-send: relative path with no workspace, skipping",
					"path", p.RawPath, "channel", ch.Name())
				continue
			}
			path = filepath.Join(workspace, path)
		}
		mime, ok := detectImageFile(path)
		if !ok {
			logger.Warn("image-send: file missing or not an image, skipping",
				"path", path, "channel", ch.Name())
			continue
		}
		ref := ImageRef{Path: path, Alt: p.Alt, Mime: mime}
		if err := sender.SendImage(ctx, replyTo, ref); err != nil {
			logger.Warn("image-send: SendImage failed",
				"path", path, "channel", ch.Name(), "err", err)
		}
	}
}

// detectImageFile verifies that path exists, is a regular file, and contains
// real image magic bytes (not just a filename with an image extension).
// Returns (mime, true) on success or ("", false) on any failure.
func detectImageFile(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := f.Read(head)
	if n == 0 {
		return "", false
	}
	mime := http.DetectContentType(head[:n])
	// Strip any "; charset=..." suffix.
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	if !strings.HasPrefix(mime, "image/") {
		return "", false
	}
	return mime, true
}
