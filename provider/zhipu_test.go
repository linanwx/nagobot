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
		"glm-5.3":            true,
		"glm-5.3-flash":      true,
		"glm-5.3-flash[max]": true,
		"glm-5.3[low]":       true,
		"unknown":            false,
		"unknown[max]":       false,
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
	// Every [effort] variant must be a first-class model type. An unregistered
	// one is rejected by ValidateProviderModelType, so the routing rule naming
	// it fails the turn; a registered one missing from ContextWindows silently
	// gets a zero window instead.
	want := []string{
		"glm-5.3", "glm-5.3-flash",
		"glm-5.3[low]", "glm-5.3[high]", "glm-5.3[max]",
		"glm-5.3-flash[low]", "glm-5.3-flash[high]", "glm-5.3-flash[max]",
	}
	for _, p := range []string{"zhipu-cn", "zhipu-global"} {
		for _, m := range want {
			if err := ValidateProviderModelType(p, m); err != nil {
				t.Errorf("ValidateProviderModelType(%q, %q) = %v, want nil", p, m, err)
			}
			if got := ContextWindowForModel(p, m); got != 1000000 {
				t.Errorf("ContextWindowForModel(%q, %q) = %d, want 1000000", p, m, got)
			}
		}
		// A tier outside the vendor's enum must not be quietly accepted and
		// then dropped to the default — the rule is a typo and should fail.
		if err := ValidateProviderModelType(p, "glm-5.3-flash[medium]"); err == nil {
			t.Errorf("ValidateProviderModelType(%q, \"glm-5.3-flash[medium]\") = nil, want an error: this family rejects that tier with a 400", p)
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
	// The [effort] variants are included deliberately: SupportsVision is keyed
	// on the nagobot-facing modelType, bracket and all, so a variant left out
	// of VisionModels drops every image with no error anywhere.
	for _, tc := range []struct{ provider, model string }{
		{"zhipu-cn", "glm-5.3-flash"},
		{"zhipu-global", "glm-5.3-flash"},
		{"zhipu-cn", "glm-5.3-flash[low]"},
		{"zhipu-cn", "glm-5.3-flash[high]"},
		{"zhipu-cn", "glm-5.3-flash[max]"},
		{"zhipu-global", "glm-5.3-flash[max]"},
		{"openrouter", "z-ai/glm-5.3-flash"},
	} {
		if !SupportsVision(tc.provider, tc.model) {
			t.Errorf("SupportsVision(%q, %q) = false, want true", tc.provider, tc.model)
		}
	}
	for _, tc := range []struct{ provider, model string }{
		{"zhipu-cn", "glm-5.3"},
		{"zhipu-global", "glm-5.3"},
		{"zhipu-cn", "glm-5.3[max]"},
		{"zhipu-global", "glm-5.3[max]"},
		{"openrouter", "z-ai/glm-5.3"},
	} {
		if SupportsVision(tc.provider, tc.model) {
			t.Errorf("SupportsVision(%q, %q) = true, want false — glm-5.3 is text-only", tc.provider, tc.model)
		}
	}
}

// TestGLM53OpenRouterRoute guards two decisions a passing request cannot show
// you: the upstream pin (Z.AI and Novita serve fp8, a Cloudflare host is listed
// at quantization "unknown" for twice the price), and the ABSENCE of an effort
// pin. The second is the easier one to undo by accident — re-adding a "high"
// here looks like a harmless cost tweak and silently drops this route below the
// vendor default, which is where the no-reasoning defect came from.
func TestGLM53OpenRouterRoute(t *testing.T) {
	for _, model := range []string{"z-ai/glm-5.3", "z-ai/glm-5.3-flash"} {
		meta, ok := openRouterModels[model]
		if !ok {
			t.Fatalf("%s has no openRouterModels entry: it would ship with the zero-value meta, so no upstream pin", model)
		}
		if len(meta.ProviderOrder) == 0 || meta.ProviderOrder[0] != "z-ai" {
			t.Errorf("%s: ProviderOrder = %v, want first entry \"z-ai\"", model, meta.ProviderOrder)
		}
		if len(meta.ThinkingOpts) != 0 {
			t.Errorf("%s: ThinkingOpts is non-empty — this route must send no effort so the vendor default (max) applies; the tier is chosen per rule on the native route", model)
		}
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
				t.Errorf("%s: thinking.clear_thinking = %v, want false — Preserved Thinking, which toOpenAIChatMessages feeds by echoing reasoning_content back", model, thinking["clear_thinking"])
			}
		}
		if got := body["temperature"]; got != float64(1) {
			t.Errorf("%s: temperature = %v, want 1 — thinking is on, which forces it", model, got)
		}
	}
}

// TestZhipuEffortTierRidesTheBracket asserts on the marshalled body for the
// same reason the test above does: this parameter has been silently dropped
// twice in this codebase (zhipu's extra_body wrapper, deepseek's nested
// placement), and both times every cheaper check passed — the provider built,
// the request returned 200, the model answered, the tests stayed green.
//
// The absent case is the load-bearing one. A bare alias must send NO
// reasoning_effort key at all, because on this family the vendor default (max)
// is the DEEPEST tier and any value we could name is shallower. Sending
// "high" here is what produced 0 reasoning tokens on 85% of tool-calling turns
// in production.
func TestZhipuEffortTierRidesTheBracket(t *testing.T) {
	for _, tc := range []struct {
		modelType string
		wantWire  string
		wantEffor any // nil = the key must be absent
	}{
		{"glm-5.3-flash", "glm-5.3-flash", nil},
		{"glm-5.3", "glm-5.3", nil},
		{"glm-5.3-flash[low]", "glm-5.3-flash", "low"},
		{"glm-5.3-flash[high]", "glm-5.3-flash", "high"},
		{"glm-5.3-flash[max]", "glm-5.3-flash", "max"},
		{"glm-5.3[max]", "glm-5.3", "max"},
		// Not in the vendor enum: fall through to the default rather than
		// forwarding a value the endpoint answers with a 400.
		{"glm-5.3-flash[medium]", "glm-5.3-flash", nil},
	} {
		body := captureZhipuRequestBody(t, tc.modelType)

		// A bracket on the wire model name is a 400 from the endpoint, so the
		// suffix must never survive into "model".
		if got := body["model"]; got != tc.wantWire {
			t.Errorf("%s: wire model = %v, want %q — the [effort] bracket must be stripped before the request", tc.modelType, got, tc.wantWire)
		}

		got, present := body["reasoning_effort"]
		if tc.wantEffor == nil {
			if present {
				t.Errorf("%s: reasoning_effort = %v, want the key ABSENT so the vendor default (max) applies", tc.modelType, got)
			}
			continue
		}
		if !present {
			t.Errorf("%s: no reasoning_effort in %v, want %q", tc.modelType, keysOf(body), tc.wantEffor)
		} else if got != tc.wantEffor {
			t.Errorf("%s: reasoning_effort = %v, want %q", tc.modelType, got, tc.wantEffor)
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
