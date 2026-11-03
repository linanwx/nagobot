package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseModelEffort(t *testing.T) {
	cases := []struct{ in, base, effort string }{
		{"gpt-5.6-sol[low]", "gpt-5.6-sol", "low"},
		{"gpt-5.6-terra[xhigh]", "gpt-5.6-terra", "xhigh"},
		{"gpt-6-luna[max]", "gpt-6-luna", "max"},
		{"gpt-5.6-luna", "gpt-5.6-luna", ""},
		{"gpt-5.5", "gpt-5.5", ""},
	}
	for _, c := range cases {
		base, effort := parseModelEffort(c.in)
		if base != c.base || effort != c.effort {
			t.Errorf("parseModelEffort(%q) = (%q, %q), want (%q, %q)", c.in, base, effort, c.base, c.effort)
		}
	}
}

// TestSubscriptionModelCatalog pins openai-oauth to what the subscription
// route actually serves (GET /v1/models with a Sign in with ChatGPT token),
// with exactly the effort tiers that catalog lists per model. gpt-5.5 and
// gpt-5.4 are not offered there; registering them would let a model rule pass
// validation and then fail every turn.
func TestSubscriptionModelCatalog(t *testing.T) {
	want := map[string][]string{
		"gpt-6.1-sol":   effortsToUltra,
		"gpt-6.1-terra": effortsToUltra,
		"gpt-6-sol":     effortsToUltra,
		"gpt-6-astra":   effortsToUltra,
		"gpt-6-luna":    effortsToMax,
		"gpt-5.6-sol":   effortsToUltra,
		"gpt-5.6-terra": effortsToUltra,
		"gpt-5.6-luna":  effortsToMax,
	}
	for model, efforts := range want {
		for _, variant := range append([]string{model}, bracketed(model, efforts)...) {
			if err := ValidateProviderModelType("openai-oauth", variant); err != nil {
				t.Errorf("openai-oauth %s: %v", variant, err)
			}
			if w := ContextWindowForModel("openai-oauth", variant); w != longContextPriceBreak {
				t.Errorf("openai-oauth %s window = %d, want %d", variant, w, longContextPriceBreak)
			}
			if !SupportsVision("openai-oauth", variant) {
				t.Errorf("openai-oauth %s is not vision-capable", variant)
			}
		}
	}
	for _, absent := range []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-6-luna[ultra]"} {
		if err := ValidateProviderModelType("openai-oauth", absent); err == nil {
			t.Errorf("openai-oauth must not register %s", absent)
		}
	}
	// gpt-5.5 has no effort variants on the API-key route either.
	if err := ValidateProviderModelType("openai", "gpt-5.5[low]"); err == nil {
		t.Error("gpt-5.5[low] must not be registered")
	}
}

func bracketed(model string, efforts []string) []string {
	var out []string
	for _, e := range efforts {
		out = append(out, model+"["+e+"]")
	}
	return out
}

func newTestSubscriptionProvider(t *testing.T, modelType string, maxTokens int, temperature float64) *OpenAIProvider {
	t.Helper()
	reg, ok := GetProviderRegistration("openai-oauth")
	if !ok {
		t.Fatal("openai-oauth not registered")
	}
	p, ok := reg.Constructor("token", "https://ignored.example", modelType, modelType, maxTokens, temperature).(*OpenAIProvider)
	if !ok {
		t.Fatal("openai-oauth constructor did not return *OpenAIProvider")
	}
	return p
}

// TestSubscriptionRequestBody pins the parameters the subscription route
// accepts and rejects, as probed live: temperature is a 400 there,
// max_output_tokens is honored, and store must be false.
func TestSubscriptionRequestBody(t *testing.T) {
	p := newTestSubscriptionProvider(t, "gpt-6-luna", 4096, 0.7)
	if p.baseURL != openAIAPIBase {
		t.Fatalf("openai-oauth baseURL = %q, want %q regardless of the configured apiBase", p.baseURL, openAIAPIBase)
	}
	body, err := p.buildRequestBody(WithSessionKey(context.Background(), "discord:1"), &Request{
		Messages: []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := body["temperature"]; ok {
		t.Error("subscription request carries temperature; the route answers 400 Unsupported parameter")
	}
	if got := body["max_output_tokens"]; got != 4096 {
		t.Errorf("max_output_tokens = %v, want 4096", got)
	}
	if body["store"] != false {
		t.Errorf("store = %v, want false", body["store"])
	}
	if _, ok := body["previous_response_id"]; ok {
		t.Error("previous_response_id must never be sent: with store=false there is nothing to chain from")
	}
	if body["prompt_cache_key"] == nil {
		t.Error("prompt_cache_key missing for a request with a session key")
	}

	api := newOpenAIProvider("key", "", "gpt-5.5", "gpt-5.5", 4096, 0.7)
	apiBody, _ := api.buildRequestBody(context.Background(), &Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if apiBody["temperature"] != 0.7 {
		t.Errorf("API-key request temperature = %v, want 0.7", apiBody["temperature"])
	}
}

// TestSubscriptionSendsSessionAffinityHeaders: without these three headers the
// subscription route caches nothing (measured 0 cached tokens against 14208
// with them on a repeated 14K prefix), so their absence is a silent cost
// regression, not an error anyone would see.
func TestSubscriptionSendsSessionAffinityHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := WithSessionKey(context.Background(), "discord:1")
	want := sessionCacheKey(ctx)

	sub := newTestSubscriptionProvider(t, "gpt-6-luna", 0, 0)
	resp, err := sub.doRequest(ctx, srv.URL, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, h := range []string{"session-id", "thread-id", "x-client-request-id"} {
		if got.Get(h) != want {
			t.Errorf("subscription header %s = %q, want %q", h, got.Get(h), want)
		}
	}
	if got.Get("ChatGPT-Account-ID") != "" {
		t.Error("ChatGPT-Account-ID belongs to the retired Codex backend")
	}

	api := newOpenAIProvider("key", "", "gpt-5.5", "gpt-5.5", 0, 0)
	resp, err = api.doRequest(ctx, srv.URL, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.Get("session-id") != "" {
		t.Error("API-key requests must not carry the subscription affinity headers")
	}
}

// TestChildSessionsShareTheRootCacheKey: subagents of one session send the same
// tools + system prefix, so they must land on the root session's cache shard,
// or a sibling's first call misses a prefix cached minutes earlier.
func TestChildSessionsShareTheRootCacheKey(t *testing.T) {
	key := func(s string) string { return sessionCacheKey(WithSessionKey(context.Background(), s)) }
	root := key("discord:1")
	for _, child := range []string{"discord:1:threads:a", "discord:1:threads:b", "discord:1:fork:c", "discord:1:threads:a:fork:d"} {
		if got := key(child); got != root {
			t.Errorf("cache key for %s = %s, want the root's %s", child, got, root)
		}
	}
	if key("discord:2:threads:a") == root {
		t.Error("children of different sessions must not share a cache key")
	}
	if key("discord:1:progresssummary") == root {
		t.Error("sibling sessions run a different agent and keep their own key")
	}
}

// TestSubscriptionErrorsAreLegible covers both places a subscription error can
// arrive: as the HTTP error body before streaming, and as response.failed
// after it has started. Either way the person must be told what happened to
// their plan, not shown a raw status line.
func TestSubscriptionErrorsAreLegible(t *testing.T) {
	const code = "subscription_sharing_usage_limit_exceeded"

	t.Run("http body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":{"code":"`+code+`","message":"limit reached"}}`)
		}))
		defer srv.Close()
		p := newTestSubscriptionProvider(t, "gpt-6-luna", 0, 0)
		p.baseURL = srv.URL
		res, err := p.Chat(context.Background(), &Request{Messages: []Message{{Role: "user", Content: "hi"}}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = res.Wait()
		if err == nil || !strings.Contains(err.Error(), "usage limit is reached") || !strings.Contains(err.Error(), code) {
			t.Fatalf("error = %v, want the usage-limit explanation with its code", err)
		}
	})

	t.Run("response.failed", func(t *testing.T) {
		a := newResponseAssembler(newStreamAdapter(context.Background(), &Response{}))
		_, err := a.handleEvent([]byte(`{"type":"response.failed","response":{"error":{"code":"` + code + `","message":"limit reached"}}}`))
		if err == nil || !strings.Contains(err.Error(), "usage limit is reached") {
			t.Fatalf("error = %v, want the usage-limit explanation", err)
		}
	})
}
