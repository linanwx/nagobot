package opencodego

import (
	"encoding/json"
	"testing"
)

func TestStepFreeRouteOutputLimitAndImages(t *testing.T) {
	for _, test := range []struct{ requested, want int }{
		{0, 65536}, {2048, 2048}, {65536, 65536}, {128000, 65536},
	} {
		wire, err := chatRequest(Request{Model: "step-5-preview-free", MaxTokens: test.requested,
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
		if body["model"] != "step-5-preview-free" || body["max_tokens"] != float64(test.want) {
			t.Fatalf("free route identity/limit changed: %s", payload)
		}
		for _, field := range []string{"reasoning_effort", "temperature", "thinking"} {
			if _, exists := body[field]; exists {
				t.Fatalf("bare Step inherited parameter %s", field)
			}
		}
		parts := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
		if parts[1].(map[string]any)["image_url"].(map[string]any)["url"] != "data:image/png;base64,eA==" {
			t.Fatalf("Step image input lost: %s", payload)
		}
	}
	// The free-route cap must not change existing Go models' configured output.
	other, err := chatRequest(Request{Model: "glm-5.3", MaxTokens: 128000})
	if err != nil || other.MaxTokens != 128000 {
		t.Fatalf("Step policy leaked into GLM: %+v %v", other, err)
	}
}
