package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// webFetchMaxFileBytes caps a downloaded non-page file. It is deliberately far
// larger than webFetchMaxReadBytes, which bounds text going INTO the
// conversation — these bytes go to disk and never enter the context window.
// 20 MB matches the channel media downloader's cap.
const webFetchMaxFileBytes = 20 << 20

// NonPageContent reports that a URL served a file rather than a web page — a
// PDF, an image, an archive, an Office document — and that the bytes were saved
// to disk instead of being forced through HTML extraction.
//
// It travels as an error because that is the honest answer to the provider's
// contract ("return this page's text"): there is no page text. Callers
// recognize it with errors.As and turn it into a successful tool result naming
// the saved file. The alternative — widening FetchProvider.Fetch's return —
// would change all five providers to serve the two that do their own HTTP.
//
// Without this path a PDF fetched through `raw` reached extractTextContent,
// which ran an HTML parser over binary and returned up to webFetchMaxContentChars
// of garbage into the conversation, cached for ten minutes.
type NonPageContent struct {
	Path string // absolute path of the saved file
	MIME string // effective content type (declared, or sniffed when unlabelled)
	Size int64  // bytes written
}

func (e *NonPageContent) Error() string {
	return fmt.Sprintf("not a web page: %s saved to %s", e.MIME, e.Path)
}

// saveIfNotPage classifies a fetch response and, when it carries a file rather
// than a web page, streams it into the workspace media directory.
//
// On a page it returns a reader replaying the bytes it consumed for sniffing,
// so the caller reads the body as if untouched. On a file it returns a
// *NonPageContent and no reader — the body has been drained to disk.
//
// Providers that fetch over their own HTTP call this right after the status
// check. Remote extractor providers (jina, kimi) do not: they return text their
// service already produced, and jina in particular extracts PDFs itself.
func saveIfNotPage(ctx context.Context, resp *http.Response, rawURL string) (io.Reader, *NonPageContent, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(resp.Body, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, nil, err
	}
	head = head[:n]
	body := io.MultiReader(bytes.NewReader(head), resp.Body)

	ct := resolveContentType(resp.Header.Get("Content-Type"), head)
	if isPageContentType(ct, head) {
		return body, nil, nil
	}

	rt := RuntimeContextFrom(ctx)
	if rt.Workspace == "" {
		// Fail loud rather than falling back to the old behaviour. Returning
		// these bytes as "page text" is precisely what this function exists to
		// prevent, so doing it quietly when the workspace is missing would
		// reintroduce the defect exactly where it is hardest to notice.
		return nil, nil, fmt.Errorf("URL served %s, not a web page, and no workspace is available to save it to", ct)
	}

	np, err := saveFetchedFile(rt.Workspace, rawURL, ct, body)
	if err != nil {
		return nil, nil, err
	}
	return nil, np, nil
}

// resolveContentType picks the effective content type. The declared header
// wins, except when it is absent or the catch-all application/octet-stream —
// which is what a great many hosts label a PDF download as — in which case the
// bytes decide. http.DetectContentType itself falls back to octet-stream, so a
// sniff that learns nothing leaves the declared value alone rather than
// inventing one.
func resolveContentType(declared string, head []byte) string {
	ct := strings.ToLower(strings.TrimSpace(declared))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if ct != "" && ct != "application/octet-stream" {
		return ct
	}
	if len(head) > 0 {
		sniffed := http.DetectContentType(head)
		if i := strings.IndexByte(sniffed, ';'); i >= 0 {
			sniffed = strings.TrimSpace(sniffed[:i])
		}
		if sniffed != "" && sniffed != "application/octet-stream" {
			return sniffed
		}
	}
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}

// isPageContentType reports whether a response should take the normal
// text/markdown extraction path.
//
// The bias is deliberately toward "page": this predicate can only divert
// content away from behaviour that already works, so anything readable as text
// stays where it is. Only formats that HTML extraction would destroy get
// downloaded.
func isPageContentType(ct string, head []byte) bool {
	switch ct {
	case "text/html", "application/xhtml+xml",
		"application/json", "application/xml", "text/xml",
		"application/javascript", "application/rss+xml", "application/atom+xml":
		return true
	}
	// text/plain, text/markdown, text/csv — all readable inline, and the model
	// can already act on them.
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	// An unlabelled response that reads as text is text somebody forgot to
	// declare, not a file.
	if ct == "application/octet-stream" && looksLikeText(head) {
		return true
	}
	return false
}

// looksLikeText applies DetectFileType's text test to a response prefix: no NUL
// bytes and valid UTF-8. An empty body counts as text so a blank response keeps
// its existing behaviour instead of being saved as a zero-byte file.
func looksLikeText(head []byte) bool {
	if len(head) == 0 {
		return true
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	// Trim a trailing partial rune left by the fixed-size read.
	for len(head) > 0 && !utf8.Valid(head) {
		head = head[:len(head)-1]
	}
	return len(head) > 0
}

// saveFetchedFile streams body into {workspace}/media under a generated name.
//
// That directory is the one channels download into, read_file reads from, and
// the web console serves — so a fetched file is immediately usable by every
// existing path, including an inline ![](media/…) reference, with no new
// convention to learn.
func saveFetchedFile(workspace, rawURL, contentType string, body io.Reader) (*NonPageContent, error) {
	dir := filepath.Join(workspace, "media")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create media directory: %w", err)
	}

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return nil, fmt.Errorf("generate file name: %w", err)
	}
	name := fmt.Sprintf("fetch-%s-%s%s",
		time.Now().Format("20060102-150405"),
		hex.EncodeToString(suffix),
		fetchFileExt(rawURL, contentType))
	path := filepath.Join(dir, name)

	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}
	// Read one byte past the cap so an oversized file is DETECTED rather than
	// silently truncated. A truncated PDF is a corrupt PDF: read_file would
	// accept it and pdftotext would fail somewhere downstream with an error
	// naming neither this limit nor the real cause. Content-Length is not
	// consulted — it is optional and can lie; the bytes cannot.
	size, err := io.Copy(f, io.LimitReader(body, webFetchMaxFileBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("write file: %w", err)
	}
	if size > webFetchMaxFileBytes {
		os.Remove(path)
		return nil, fmt.Errorf("file is larger than the %d MB download limit; fetch it with the exec tool instead (e.g. curl -o)",
			webFetchMaxFileBytes>>20)
	}
	return &NonPageContent{Path: path, MIME: contentType, Size: size}, nil
}

// fetchFileExt picks the saved file's extension.
//
// The extension is not cosmetic: read_file's DetectFileType consults it before
// magic bytes, so a wrong one sends the file down the wrong branch. Content
// type is therefore consulted FIRST — it is either what the server declared or
// what the bytes sniffed to — and the URL's own extension is the fallback for
// the unlabelled case, where it is the only signal left.
func fetchFileExt(rawURL, contentType string) string {
	if ext, ok := fetchContentTypeExt[contentType]; ok {
		return ext
	}
	if exts, err := mime.ExtensionsByType(contentType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	if u, err := url.Parse(rawURL); err == nil {
		if ext := strings.ToLower(filepath.Ext(u.Path)); isSimpleExt(ext) {
			return ext
		}
	}
	return ".bin"
}

// isSimpleExt guards the URL-derived fallback: the path segment is attacker- and
// typo-shaped input that ends up in a file name, so only a short alphanumeric
// suffix is accepted.
func isSimpleExt(ext string) bool {
	if len(ext) < 2 || len(ext) > 6 || ext[0] != '.' {
		return false
	}
	for _, r := range ext[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// fetchContentTypeExt covers the types worth naming exactly. Anything absent
// falls through to mime.ExtensionsByType, which depends on the host's mime.types
// and cannot be relied on inside a slim container.
var fetchContentTypeExt = map[string]string{
	"application/pdf": ".pdf",

	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
	"image/bmp":  ".bmp",

	"audio/ogg":   ".ogg",
	"audio/mpeg":  ".mp3",
	"audio/mp4":   ".m4a",
	"audio/wav":   ".wav",
	"audio/x-wav": ".wav",
	"audio/flac":  ".flac",
	"audio/aac":   ".aac",

	"video/mp4":       ".mp4",
	"video/webm":      ".webm",
	"video/quicktime": ".mov",

	"application/zip":              ".zip",
	"application/gzip":             ".gz",
	"application/x-gzip":           ".gz",
	"application/x-tar":            ".tar",
	"application/x-7z-compressed":  ".7z",
	"application/vnd.rar":          ".rar",
	"application/x-rar-compressed": ".rar",

	"application/msword":            ".doc",
	"application/vnd.ms-excel":      ".xls",
	"application/vnd.ms-powerpoint": ".ppt",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   ".docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ".xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
}
