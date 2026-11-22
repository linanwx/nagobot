package opencodego

import (
	"encoding/json"
	"testing"
)

// Hy4 flash Free is a Chat Completions free route: the exact Go id is sent,
// output is capped at the route's 65,536 limit, effort passes through as
// reasoning_effort (none|high), and images ride as image_url parts.
func TestHy4FlashFreeWireRequest(t *testing.T) {
	for _, test := range []struct {
		requested, want int
		effort          string
	}{
		{0, 65536, ""}, {2048, 2048, "none"}, {65536, 65536, "high"}, {128000, 65536, ""},
	} {
		wire, err := chatRequest(Request{Model: "hy4-flash-free", MaxTokens: test.requested, Effort: test.effort,
			Messages: []Message{{Role: "user", Content: "inspect", Images: []Image{{MIMEType: "image/png", Data: "eA=="}}}}})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "hy4-flash-free" || body["max_tokens"] != float64(test.want) {
			t.Fatalf("free route identity/limit changed: %s", payload)
		}
		if effort, exists := body["reasoning_effort"]; test.effort == "" && exists || test.effort != "" && effort != test.effort {
			t.Fatalf("effort %q not sent as reasoning_effort: %s", test.effort, payload)
		}
		for _, field := range []string{"temperature", "thinking"} {
			if _, exists := body[field]; exists {
				t.Fatalf("Hy4 inherited parameter %s", field)
			}
		}
		parts := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
		if parts[1].(map[string]any)["image_url"].(map[string]any)["url"] != "data:image/png;base64,eA==" {
			t.Fatalf("Hy4 image input lost: %s", payload)
		}
	}
}

func TestHy4FlashFreeRejectsUnlistedEffort(t *testing.T) {
	for _, effort := range []string{"low", "medium", "max"} {
		if _, err := NewClient("key", "http://127.0.0.1:1").Chat(t.Context(), "", Request{Model: "hy4-flash-free", Effort: effort}, nil); err == nil {
			t.Fatalf("effort %q accepted for hy4-flash-free", effort)
		}
	}
}
