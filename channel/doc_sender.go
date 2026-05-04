package channel

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"github.com/linanwx/nagobot/logger"
)

type DocRef struct {
	Path  string
	Label string
	Mime  string
	Name  string
}

// DocSender is the optional capability that lets a channel deliver
// Markdown link references to local files parsed from a regular text
// response (e.g. `[report](media/q1.pdf)`).
type DocSender interface {
	SendDoc(ctx context.Context, replyTo string, ref DocRef) error
}

// dispatchDocRefs parses text for Markdown link references whose targets
// are local files, resolves each against workspace (if relative), validates
// the file exists, and calls ch.SendDoc for each surviving ref. Errors are
// logged but never returned — doc delivery is a best-effort side-effect on
// top of the already-delivered text.
//
// If ch does not implement DocSender, this is a no-op.
// If workspace is "", relative paths are skipped (logged at WARN).
func dispatchDocRefs(ctx context.Context, ch Channel, replyTo, text, workspace string) {
	sender, ok := ch.(DocSender)
	if !ok {
		return
	}
	parsed := parseMarkdownDocs(text)
	for _, p := range parsed {
		path := p.RawPath
		if !filepath.IsAbs(path) {
			if workspace == "" {
				logger.Warn("doc-send: relative path with no workspace, skipping",
					"path", p.RawPath, "channel", ch.Name())
				continue
			}
			path = filepath.Join(workspace, path)
		}
		mime, ok := detectDocFile(path)
		if !ok {
			// Silently skip — matches send-image behaviour. The parser already
			// filtered URLs/anchors; a missing file is most often the LLM
			// writing a regular Markdown link to text content.
			continue
		}
		ref := DocRef{
			Path:  path,
			Label: p.Label,
			Mime:  mime,
			Name:  filepath.Base(path),
		}
		if err := sender.SendDoc(ctx, replyTo, ref); err != nil {
			logger.Warn("doc-send: SendDoc failed",
				"path", path, "channel", ch.Name(), "err", err)
		}
	}
}

// detectDocFile verifies the target is a regular file we can read,
// and returns its detected MIME type. Image MIME is intentionally
// allowed — `[label](path)` to an image is a valid attachment intent;
// the image dispatcher only fires on `![alt](path)` syntax.
func detectDocFile(path string) (string, bool) {
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
		// Empty files have no useful content — skip rather than upload zero bytes.
		return "", false
	}
	return http.DetectContentType(head[:n]), true
}
