package provider

import (
	"strings"
	"testing"
)

// An avif in history was attached as an image; the API rejected the whole
// request, and since the marker stays in history every later turn of that
// session failed. ParseMediaMarkers is the one door every provider goes
// through, so the filter lives there.
func TestParseMediaMarkersDropsUnsupportedImages(t *testing.T) {
	text := "ok\n<<media:image/avif:/w/media/a.avif>>\n<<media:image/png:/w/media/b.png>>\n<<media:audio/ogg:/w/media/c.ogg>>"
	cleaned, markers := ParseMediaMarkers(text)
	if len(markers) != 2 || markers[0].MimeType != "image/png" || markers[1].MimeType != "audio/ogg" {
		t.Fatalf("markers = %+v, want png and ogg only", markers)
	}
	if !strings.Contains(cleaned, "image not attached: image/avif") || !strings.Contains(cleaned, "/w/media/a.avif") {
		t.Fatalf("cleaned text lacks the avif note: %q", cleaned)
	}
	if strings.Contains(cleaned, "<<media:") {
		t.Fatalf("a marker survived in cleaned text: %q", cleaned)
	}
}

func TestStreamErrorEventFails(t *testing.T) {
	a := &responseAssembler{adapter: &streamAdapter{resp: &Response{}}, resp: &Response{}}
	_, err := a.handleEvent([]byte(`{"type":"error","code":"invalid_image_format","message":"bad image"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid_image_format") {
		t.Fatalf("want stream error, got %v", err)
	}
}
