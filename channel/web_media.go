package channel

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleMedia serves files from {workspace}/media at /api/media/{name}.
// Auth-protected (wrapped by protected() in Start). Lookup is by basename
// only — wake frontmatter carries absolute image_path values, but the client
// sends just the final path element, so traversal cannot escape the media
// directory.
func (w *WebChannel) handleMedia(rw http.ResponseWriter, r *http.Request) {
	if w.workspace == "" {
		http.Error(rw, "workspace unavailable", http.StatusInternalServerError)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/media/")
	name = filepath.Base(filepath.Clean(name))
	if name == "" || name == "." || name == string(filepath.Separator) {
		http.Error(rw, "missing file name", http.StatusBadRequest)
		return
	}
	path := filepath.Join(w.workspace, "media", name)
	// http.ServeFile sets Content-Type from the extension and handles
	// range requests (audio/video seeking) for free.
	http.ServeFile(rw, r, path)
}

// handleMediaUpload accepts a raw image body at POST /api/media, writes it into
// {workspace}/media, and returns {"name": "<basename>"}. The name is what the
// client then attaches to its next "message" WS frame (as `media`), which the
// message handler turns into a media_summary — identical to how Telegram/Discord
// attach a downloaded photo. Auth-protected (wrapped by protected() in Start).
// Only image/* is accepted; other types are rejected so the console can't be
// used as a generic file drop.
func (w *WebChannel) handleMediaUpload(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.workspace == "" {
		http.Error(rw, "workspace unavailable", http.StatusInternalServerError)
		return
	}

	contentType := strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0])
	if !strings.HasPrefix(contentType, "image/") {
		http.Error(rw, "only image uploads are supported", http.StatusUnsupportedMediaType)
		return
	}

	mediaDir := filepath.Join(w.workspace, "media")
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		http.Error(rw, "failed to prepare media directory", http.StatusInternalServerError)
		return
	}

	// http.MaxBytesReader caps the body at the same 20 MB ceiling saveMediaFile
	// enforces, so an oversize upload is refused at the transport layer too.
	name, err := saveMediaFile(mediaDir, contentType, http.MaxBytesReader(rw, r.Body, 20<<20))
	if err != nil {
		http.Error(rw, "upload failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(map[string]string{"name": name})
}
