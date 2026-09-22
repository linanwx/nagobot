package channel

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// mediaCacheControl keeps a served media file out of the network for a week.
// `private` because this sits behind protected() and Caddy fronts the
// deployments — no intermediary should hold a per-user response. No
// `immutable`: most names carry a timestamp+hash and never change, but some
// (a per-video cover, say) can legitimately be re-downloaded under the same
// name, and immutable would keep the stale bytes on screen for the whole week.
// http.ServeFile still emits Last-Modified/ETag, so the post-expiry request is
// a 304 rather than a re-transfer.
const mediaCacheControl = "private, max-age=604800"

// resolveMediaPath turns a caller-supplied path into an absolute path proven to
// live under {workspace}/media, or reports why it does not.
//
// Resolution deliberately mirrors tools.resolveToolPath — absolute stays put,
// relative joins the workspace — so the set of paths the model can `read_file`
// and the set the browser can display are the same set by construction. A
// mismatch there would mean the model referencing a file it can read but the
// page cannot show, which is exactly the confusing half-state to avoid.
//
// Containment, not path shape, is the security boundary. That is why both
// absolute and relative forms are accepted: neither is safer than the other
// once the resolved path must prove itself under the media root. The rejected
// alternative was matching on a "/media/" segment and taking the tail, which
// fails OPEN in a way that is worse than a traversal: "/home/me/photos/media/
// cover.jpg" — a path outside the workspace entirely — would map onto
// {workspace}/media/cover.jpg and silently serve an unrelated image.
//
// Symlinks are evaluated on BOTH sides before comparing. Evaluating only the
// candidate breaks on hosts where the workspace itself sits behind a link
// (macOS /tmp -> /private/tmp, the test environment), and evaluating neither
// lets a link planted inside media/ point anywhere on disk.
func resolveMediaPath(workspace, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("missing path")
	}
	if workspace == "" {
		return "", errors.New("workspace unavailable")
	}

	p := raw
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, p)
	}
	p = filepath.Clean(p)

	root := filepath.Clean(filepath.Join(workspace, "media"))
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	// A missing file has nothing to evaluate; leave it cleaned and let
	// ServeFile answer 404 rather than reporting it as an escape.
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}

	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", errors.New("path is outside the media directory")
	}
	return p, nil
}

// activeContentExtensions are the extensions a browser will execute or
// interpret as markup when served from this origin. An uploaded (or
// bot-downloaded) page must never run as same-origin script of the console.
var activeContentExtensions = map[string]bool{
	".html": true, ".htm": true, ".svg": true, ".xhtml": true, ".xml": true,
}

// serveMediaFile resolves raw and writes the file, or the reason it will not.
func (w *WebChannel) serveMediaFile(rw http.ResponseWriter, r *http.Request, raw string) {
	path, err := resolveMediaPath(w.workspace, raw)
	if err != nil {
		// 403, not 404: the path was understood and refused. The client turns
		// this into a visible "not under media/" message instead of a broken
		// image icon, so a convention violation is legible rather than silent.
		http.Error(rw, err.Error(), http.StatusForbidden)
		return
	}
	rw.Header().Set("Cache-Control", mediaCacheControl)
	// Media now includes user-uploaded files, so the serve side must not let
	// one execute in the console's origin. nosniff stops content-type
	// sniffing, and re-typing active-content files as text/plain stops the
	// browser from parsing them at all — the bytes stay intact for download
	// and read_file, which is all anyone legitimately does with them.
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	if activeContentExtensions[strings.ToLower(filepath.Ext(path))] {
		rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	// http.ServeFile sets Content-Type from the extension and handles
	// range requests (audio/video seeking) for free — and leaves a
	// pre-set Content-Type alone, which the override above relies on.
	http.ServeFile(rw, r, path)
}

// handleMedia serves /api/media/{subpath}, where subpath is relative to
// {workspace}/media. Subdirectories are allowed: the bot creates them itself
// (media/bilibili-cover/…), and the previous basename-only lookup made every
// one of those files unreachable — the URL resolved to a sibling that does not
// exist. Auth-protected (wrapped by protected() in Start).
//
// The upload round-trip is unaffected: handleMediaUpload returns a basename,
// which is still a valid subpath.
func (w *WebChannel) handleMedia(rw http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(r.URL.Path, "/api/media/")
	if sub == "" {
		http.Error(rw, "missing file name", http.StatusBadRequest)
		return
	}
	w.serveMediaFile(rw, r, filepath.Join("media", sub))
}

// handleMediaUpload accepts a raw file body at POST /api/media, writes it into
// {workspace}/media, and returns {"name": "<basename>"}. The name is what the
// client then attaches to its next "message" WS frame (as `media`), which the
// message handler turns into a media_summary — identical to how Telegram/Discord
// attach a downloaded photo. Auth-protected (wrapped by protected() in Start).
//
// Accepted types are anything saveMediaFile can resolve to an allowed
// extension: from the ?filename= query parameter (the original file name — the
// body is raw bytes, so the name cannot ride a multipart field) or, failing
// that, from the Content-Type. That keeps the endpoint a media pipe rather
// than a generic file drop: unknown or disallowed types (executables, video,
// …) are refused with 415, and the 20 MB body cap stands.
// GET /api/media?path=… is the read side that shares this route. It exists
// because the paths the model writes cannot ride in a URL path segment: an
// absolute one would produce "/api/media//root/…", whose double slash the mux
// normalizes away. The client therefore does NO path interpretation at all —
// it hands over what the model wrote, verbatim and URL-encoded, and the server,
// the only party that knows where the workspace is, makes the single decision.
func (w *WebChannel) handleMediaUpload(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if raw := r.URL.Query().Get("path"); raw != "" {
			w.serveMediaFile(rw, r, raw)
			return
		}
	}
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.workspace == "" {
		http.Error(rw, "workspace unavailable", http.StatusInternalServerError)
		return
	}

	contentType := strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0])
	// The name is used only to resolve an extension, but a path-shaped value
	// must still never survive the boundary; basename-clean it here once.
	filename := filepath.Base(filepath.Clean(r.URL.Query().Get("filename")))

	mediaDir := filepath.Join(w.workspace, "media")
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		http.Error(rw, "failed to prepare media directory", http.StatusInternalServerError)
		return
	}

	// http.MaxBytesReader caps the body at the same 20 MB ceiling saveMediaFile
	// enforces, so an oversize upload is refused at the transport layer too.
	name, err := saveMediaFile(mediaDir, filename, contentType, http.MaxBytesReader(rw, r.Body, 20<<20))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errUnsupportedFileType) {
			status = http.StatusUnsupportedMediaType
		}
		http.Error(rw, "upload failed: "+err.Error(), status)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(map[string]string{"name": name})
}

// webFilename sanitizes the client-supplied original name for the
// media_summary: basename-cleaned, control characters stripped (a newline in
// file_name could forge a whole summary line the model would trust), and
// capped at a sane display length. Empty result means "no name to show".
func webFilename(raw string) string {
	name := truncateRunes(filepath.Base(filepath.Clean(strings.TrimSpace(raw))), 120)
	var b strings.Builder
	for _, r := range name {
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == string(filepath.Separator) {
		return ""
	}
	return out
}

// webMediaSummary classifies a stored upload by the extension of the file on
// disk — not the client's mime string, which is spoofable — into the same
// media_summary shapes Telegram/Discord/WeCom emit. Image/audio path keys hook
// the dispatcher's preview agents; document/file paths route the model to
// read_file. The second return reports whether this is an image, which the
// message handler uses to pick the caption-less placeholder text.
func webMediaSummary(workspace, storedName, originalName string) (summary string, isImage bool) {
	path := filepath.Join(workspace, "media", storedName)
	ext := strings.ToLower(filepath.Ext(storedName))
	name := webFilename(originalName)
	switch {
	case imageMediaExtensions[ext]:
		// Photos keep the pre-file-upload shape (type "photo", no file_name):
		// a generated img-* name carries no information worth surfacing.
		return MediaSummary("photo", "image_path", path), true
	case audioMediaExtensions[ext]:
		return MediaSummary("audio", "file_name", name, "audio_path", path), false
	case ext == ".pdf":
		return MediaSummary("document", "file_name", name, "document_path", path), false
	}
	return MediaSummary("file", "file_name", name, "file_path", path), false
}
