package channel

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A 1x1 PNG — enough bytes for the upload handler to write and for the
// extension to resolve from the image/png content type.
var onePixelPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestHandleMediaUpload_WritesFileAndReturnsName(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")

	req := httptest.NewRequest(http.MethodPost, "/api/media", bytes.NewReader(onePixelPNG))
	req.Header.Set("Content-Type", "image/png")
	rw := httptest.NewRecorder()
	ch.handleMediaUpload(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	var resp struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rw.Body.String())
	}
	if !strings.HasPrefix(resp.Name, "img-") || !strings.HasSuffix(resp.Name, ".png") {
		t.Errorf("name = %q, want img-*.png", resp.Name)
	}

	// The returned name must resolve under {workspace}/media and hold the bytes.
	got, err := os.ReadFile(filepath.Join(ch.workspace, "media", resp.Name))
	if err != nil {
		t.Fatalf("read stored media: %v", err)
	}
	if !bytes.Equal(got, onePixelPNG) {
		t.Errorf("stored bytes differ from upload (len %d vs %d)", len(got), len(onePixelPNG))
	}

	// And handleMedia (the GET serve side) must serve it back by that basename.
	getReq := httptest.NewRequest(http.MethodGet, "/api/media/"+resp.Name, nil)
	getRW := httptest.NewRecorder()
	ch.handleMedia(getRW, getReq)
	if getRW.Code != http.StatusOK {
		t.Fatalf("GET /api/media/%s = %d, want 200", resp.Name, getRW.Code)
	}
	if !bytes.Equal(getRW.Body.Bytes(), onePixelPNG) {
		t.Errorf("served bytes differ from upload")
	}
}

func TestHandleMediaUpload_RejectsNonImage(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")

	req := httptest.NewRequest(http.MethodPost, "/api/media", strings.NewReader("plain text"))
	req.Header.Set("Content-Type", "text/plain")
	rw := httptest.NewRecorder()
	ch.handleMediaUpload(rw, req)

	if rw.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", rw.Code)
	}
}

func TestHandleMediaUpload_RejectsGet(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")

	req := httptest.NewRequest(http.MethodGet, "/api/media", nil)
	rw := httptest.NewRecorder()
	ch.handleMediaUpload(rw, req)

	if rw.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rw.Code)
	}
}

// writeMediaFixture drops a PNG at {workspace}/media/<rel> and returns its
// absolute path.
func writeMediaFixture(t *testing.T, ch *WebChannel, rel string) string {
	t.Helper()
	abs := filepath.Join(ch.workspace, "media", rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, onePixelPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	return abs
}

// A subdirectory under media/ must be reachable. The bot creates these itself
// (media/bilibili-cover/…); under the old basename-only lookup every such file
// resolved to a nonexistent sibling and 404'd.
func TestHandleMedia_ServesSubdirectory(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	writeMediaFixture(t, ch, "bilibili-cover/BV1ck336eESp-cover.jpg")

	req := httptest.NewRequest(http.MethodGet, "/api/media/bilibili-cover/BV1ck336eESp-cover.jpg", nil)
	rw := httptest.NewRecorder()
	ch.handleMedia(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rw.Code, rw.Body.String())
	}
	if !bytes.Equal(rw.Body.Bytes(), onePixelPNG) {
		t.Error("served bytes do not match the fixture")
	}
	if got := rw.Header().Get("Cache-Control"); got != mediaCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, mediaCacheControl)
	}
}

// Both path shapes the send-image skill permits must work, and must land on the
// same file — relative resolves against the workspace exactly as
// tools.resolveToolPath does, so what the model can read_file it can also show.
func TestHandleMediaPathQuery_AcceptsRelativeAndAbsolute(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	abs := writeMediaFixture(t, ch, "bilibili-cover/cover.jpg")

	for _, raw := range []string{"media/bilibili-cover/cover.jpg", abs} {
		req := httptest.NewRequest(http.MethodGet, "/api/media?path="+url.QueryEscape(raw), nil)
		rw := httptest.NewRecorder()
		ch.handleMediaUpload(rw, req)

		if rw.Code != http.StatusOK {
			t.Errorf("path=%q: status = %d, want 200 (body: %s)", raw, rw.Code, rw.Body.String())
			continue
		}
		if !bytes.Equal(rw.Body.Bytes(), onePixelPNG) {
			t.Errorf("path=%q: served bytes do not match the fixture", raw)
		}
	}
}

// Containment is the whole security boundary, so every way out of media/ must
// be refused — including the one a "/media/ segment" matcher would fail OPEN
// on: a path outside the workspace that merely contains a media/ component.
func TestResolveMediaPath_RejectsEscapes(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	writeMediaFixture(t, ch, "ok.png")
	outside := filepath.Join(ch.workspace, "system", "persons.json")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A real file outside the workspace whose path contains a "media" segment.
	decoy := filepath.Join(t.TempDir(), "photos", "media")
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatal(err)
	}
	decoyFile := filepath.Join(decoy, "ok.png")
	if err := os.WriteFile(decoyFile, onePixelPNG, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, raw := range []string{
		"media/../system/persons.json",
		outside,
		"/etc/passwd",
		decoyFile,
		"",
	} {
		if got, err := resolveMediaPath(ch.workspace, raw); err == nil {
			t.Errorf("resolveMediaPath(%q) = %q, want error", raw, got)
		}
	}
}

// Cleaning a path cannot see through a symlink, so a link planted inside
// media/ would otherwise hand out any file on disk.
func TestResolveMediaPath_RejectsSymlinkEscape(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	secret := filepath.Join(ch.workspace, "system", "secret.txt")
	if err := os.MkdirAll(filepath.Dir(secret), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ch.workspace, "media", "escape.txt")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if got, err := resolveMediaPath(ch.workspace, "media/escape.txt"); err == nil {
		t.Errorf("resolveMediaPath followed a symlink out of media/, got %q", got)
	}
}

// The workspace itself may sit behind a symlink (macOS /tmp -> /private/tmp is
// the common case, and t.TempDir() lands there). Evaluating only the candidate
// would then make every legitimate file look like an escape.
func TestResolveMediaPath_WorkspaceBehindSymlink(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	abs := writeMediaFixture(t, ch, "ok.png")

	if _, err := resolveMediaPath(ch.workspace, abs); err != nil {
		t.Fatalf("absolute path inside media rejected: %v", err)
	}
	if _, err := resolveMediaPath(ch.workspace, "media/ok.png"); err != nil {
		t.Fatalf("relative path inside media rejected: %v", err)
	}
}
