package channel

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
