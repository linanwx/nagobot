package channel

import (
	"net/http"
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
