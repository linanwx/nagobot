package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

func TestZhipuThinkingEnabled(t *testing.T) {
	cases := map[string]bool{
		"glm-5.3":       true,
		"glm-5.3-flash": true,
		"unknown":       false,
	}
	for model, want := range cases {
		if got := zhipuThinkingEnabled(model); got != want {
			t.Errorf("zhipuThinkingEnabled(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestZhipuRequestTemperatureForcedWhenThinking(t *testing.T) {
	// glm-5.3 enables thinking, which forces temperature to 1.
	temp, forced := zhipuRequestTemperature("glm-5.3", 0.7)
	if temp != 1 || !forced {
		t.Errorf("zhipuRequestTemperature(glm-5.3, 0.7) = (%v, %v), want (1, true)", temp, forced)
	}
	// Unknown/non-thinking models keep their configured temperature.
	temp, forced = zhipuRequestTemperature("unknown", 0.7)
	if temp != 0.7 || forced {
		t.Errorf("zhipuRequestTemperature(unknown, 0.7) = (%v, %v), want (0.7, false)", temp, forced)
	}
}

func TestZhipuGLM53Registration(t *testing.T) {
	for _, p := range []string{"zhipu-cn", "zhipu-global"} {
		for _, m := range []string{"glm-5.3", "glm-5.3-flash"} {
			if err := ValidateProviderModelType(p, m); err != nil {
				t.Errorf("ValidateProviderModelType(%q, %q) = %v, want nil", p, m, err)
			}
			if got := ContextWindowForModel(p, m); got != 1000000 {
				t.Errorf("ContextWindowForModel(%q, %q) = %d, want 1000000", p, m, got)
			}
		}
	}
}

// TestGLM53WindowsAgreeAcrossRoutes is the regression guard for the defect this
// model's registration actually shipped with: openrouter carried 262144 for
// z-ai/glm-5.3 while both native routes carried 1000000 for the same model.
// Nothing fails when a window is too small — the request is simply compressed
// and trimmed against a quarter of the real capacity — so only an assertion
// that the routes AGREE can catch it.
func TestGLM53WindowsAgreeAcrossRoutes(t *testing.T) {
	for _, tc := range []struct{ native, routed string }{
		{"glm-5.3", "z-ai/glm-5.3"},
		{"glm-5.3-flash", "z-ai/glm-5.3-flash"},
	} {
		want := ContextWindowForModel("zhipu-cn", tc.native)
		if want != 1000000 {
			t.Errorf("ContextWindowForModel(zhipu-cn, %q) = %d, want 1000000", tc.native, want)
		}
		for _, p := range []string{"zhipu-global"} {
			if got := ContextWindowForModel(p, tc.native); got != want {
				t.Errorf("ContextWindowForModel(%q, %q) = %d, want %d", p, tc.native, got, want)
			}
		}
		if got := ContextWindowForModel("openrouter", tc.routed); got != want {
			t.Errorf("ContextWindowForModel(openrouter, %q) = %d, want %d — the same model must not carry two windows",
				tc.routed, got, want)
		}
	}
}

// TestGLM53FlashSeesImages pins the capability split inside the GLM family:
// glm-5.3-flash is natively multimodal and glm-5.3 is text-only. Getting this
// wrong fails silently in both directions — an unregistered vision model drops
// every image with no error, and a registered text model sends image parts the
// upstream will reject.
func TestGLM53FlashSeesImages(t *testing.T) {
	for _, tc := range []struct{ provider, model string }{
		{"zhipu-cn", "glm-5.3-flash"},
		{"zhipu-global", "glm-5.3-flash"},
		{"openrouter", "z-ai/glm-5.3-flash"},
	} {
		if !SupportsVision(tc.provider, tc.model) {
			t.Errorf("SupportsVision(%q, %q) = false, want true", tc.provider, tc.model)
		}
	}
	for _, tc := range []struct{ provider, model string }{
		{"zhipu-cn", "glm-5.3"},
		{"zhipu-global", "glm-5.3"},
		{"openrouter", "z-ai/glm-5.3"},
	} {
		if SupportsVision(tc.provider, tc.model) {
			t.Errorf("SupportsVision(%q, %q) = true, want false — glm-5.3 is text-only", tc.provider, tc.model)
		}
	}
}

// TestGLM53FlashOpenRouterRoute guards the two decisions that a passing request
// cannot show you: the upstream pin (Z.AI and Novita serve fp8, a Cloudflare
// host is listed at quantization "unknown" for twice the price) and the effort
// dial, which the vendor doc claims this model does not have and the live API
// accepts on both models.
func TestGLM53FlashOpenRouterRoute(t *testing.T) {
	meta, ok := openRouterModels["z-ai/glm-5.3-flash"]
	if !ok {
		t.Fatal("z-ai/glm-5.3-flash has no openRouterModels entry: it would ship with the zero-value meta, so no upstream pin")
	}
	if len(meta.ProviderOrder) == 0 || meta.ProviderOrder[0] != "z-ai" {
		t.Errorf("ProviderOrder = %v, want first entry \"z-ai\"", meta.ProviderOrder)
	}
	// Empty on purpose: see the openRouterModels comment. An effort of "high"
	// measured 91/150/141 reasoning tokens against 632/798/1032 with no field.
	if len(meta.ThinkingOpts) != 0 {
		t.Errorf("ThinkingOpts = %d opts, want 0 — any effort we can send on this family is shallower than the vendor default", len(meta.ThinkingOpts))
	}
}

// TestZhipuSendsThinkingParamsAtTopLevel is the regression guard for a defect
// that shipped, ran in production for months, and returned HTTP 200 every
// single time.
//
// These two fields used to be sent under an "extra_body" wrapper. That is a
// Python-SDK convention — the Python client unwraps it before the request goes
// out — and it is not part of the wire protocol. open.bigmodel.cn ignores an
// unknown top-level object, so thinking.type and reasoning_effort were never
// applied to any request, and nothing anywhere reported it. Verified against
// the live API: extra_body.thinking.type "disabled" returns 200 and still
// thinks, while a top-level "disabled" returns 400, and a made-up field of the
// same shape behaves exactly like the extra_body one.
//
// Asserting on the marshalled body is the only way to catch this. Every check
// one level up — the provider builds, the request succeeds, the model answers —
// passed throughout.
func TestZhipuSendsThinkingParamsAtTopLevel(t *testing.T) {
	for _, model := range []string{"glm-5.3", "glm-5.3-flash"} {
		body := captureZhipuRequestBody(t, model)
		if _, wrapped := body["extra_body"]; wrapped {
			t.Errorf("%s: request carries an extra_body wrapper; the upstream ignores it and both settings are lost", model)
		}
		thinking, ok := body["thinking"].(map[string]any)
		if !ok {
			t.Errorf("%s: no top-level thinking object in %v", model, keysOf(body))
		} else {
			if thinking["type"] != "enabled" {
				t.Errorf("%s: thinking.type = %v, want \"enabled\" (the only value this family accepts)", model, thinking["type"])
			}
			if thinking["clear_thinking"] != false {
				t.Errorf("%s: thinking.clear_thinking = %v, want false — it doubles the reasoning that comes back", model, thinking["clear_thinking"])
			}
		}
		// No effort field, deliberately. "high" is BELOW the vendor's own
		// default depth on this family — measured at ~10x less reasoning than
		// sending nothing — so shipping it silently made the model think less.
		if got, present := body["reasoning_effort"]; present {
			t.Errorf("%s: reasoning_effort = %v, want the field absent — \"high\" is shallower than the server default here", model, got)
		}
		if got := body["temperature"]; got != float64(1) {
			t.Errorf("%s: temperature = %v, want 1 — thinking is on, which forces it", model, got)
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// captureZhipuRequestBody runs one Chat against a stub server and returns the
// JSON body the provider actually put on the wire.
func captureZhipuRequestBody(t *testing.T, model string) map[string]any {
	t.Helper()
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		// The provider always streams, so the stub has to speak SSE — a plain
		// JSON reply would fail the parse and the test would be asserting on a
		// body from a request that never completed.
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := newZhipuProvider("zhipu-cn", "k", srv.URL, srv.URL, model, model, 100, 0.7)
	res, err := p.Chat(context.Background(), &Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat(%s) = %v", model, err)
	}
	if _, err := res.Wait(); err != nil {
		t.Fatalf("Wait(%s) = %v", model, err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("request body for %s is not JSON: %v", model, err)
	}
	return body
}
