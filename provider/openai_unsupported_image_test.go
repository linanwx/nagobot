package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An avif in history used to be sent as input_image; the API rejected it with
// a stream error event the parser ignored, so every later turn returned an
// empty reply with no log line.
func TestUnsupportedImageBecomesNote(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.avif")
	if err := os.WriteFile(p, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	items := toolMessageToInputItems(Message{Role: "tool", ToolCallID: "c1", Content: "ok\n<<media:image/avif:" + p + ">>"})
	parts, ok := items[0]["output"].([]map[string]any)
	if !ok {
		t.Fatalf("output is %T, want content parts", items[0]["output"])
	}
	noted := false
	for _, part := range parts {
		if part["type"] == "input_image" {
			t.Fatalf("avif attached as input_image")
		}
		if txt, _ := part["text"].(string); strings.Contains(txt, "image not attached") {
			noted = true
		}
	}
	if !noted {
		t.Fatal("no note in place of the avif")
	}
}

func TestStreamErrorEventFails(t *testing.T) {
	a := &responseAssembler{adapter: &streamAdapter{resp: &Response{}}, resp: &Response{}}
	_, err := a.handleEvent([]byte(`{"type":"error","code":"invalid_image_format","message":"bad image"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid_image_format") {
		t.Fatalf("want stream error, got %v", err)
	}
}
