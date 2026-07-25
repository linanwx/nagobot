package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The handler's only job is to carry text in and text out. These tests pin that
// it stays a pass-through — no quote syntax is built, inspected or repaired here
// — and that every failure reaches the client instead of degrading quietly.

func TestHandleQuote_PassesTextThroughUntouched(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	var gotKey, gotText string
	ch.SetQuoteFn(func(_ context.Context, key, text string) (string, error) {
		gotKey, gotText = key, text
		return "> The pricing table for the three plans", nil
	})

	body := `{"session_id":"web:test","text":"| Plan | Price |\n|---|---|\n| Free | $0 |"}`
	rw := httptest.NewRecorder()
	ch.handleQuote(rw, httptest.NewRequest(http.MethodPost, "/api/quote", strings.NewReader(body)))

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	var resp quoteResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rw.Body.String())
	}
	if resp.Quote != "> The pricing table for the three plans" {
		t.Errorf("quote = %q, want the generator's line verbatim", resp.Quote)
	}
	if gotKey != "web:test" {
		t.Errorf("session key = %q, want web:test", gotKey)
	}
	if !strings.Contains(gotText, "| Plan | Price |") {
		t.Errorf("generator got %q, want the markdown unaltered", gotText)
	}
}

func TestHandleQuote_GeneratorFailureReachesTheClient(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	ch.SetQuoteFn(func(context.Context, string, string) (string, error) {
		return "", errors.New("quote generation timed out")
	})

	rw := httptest.NewRecorder()
	ch.handleQuote(rw, httptest.NewRequest(http.MethodPost, "/api/quote",
		strings.NewReader(`{"session_id":"web:test","text":"hello"}`)))

	if rw.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "timed out") {
		t.Errorf("body = %q, want the generator's reason", rw.Body.String())
	}
}

func TestHandleQuote_Rejections(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		body    string
		noQuote bool // leave quoteFn unset
		want    int
	}{
		{name: "GET", method: http.MethodGet, body: "", want: http.StatusMethodNotAllowed},
		{name: "no generator configured", method: http.MethodPost,
			body: `{"session_id":"web:test","text":"hi"}`, noQuote: true, want: http.StatusServiceUnavailable},
		{name: "malformed body", method: http.MethodPost, body: `{`, want: http.StatusBadRequest},
		{name: "blank text", method: http.MethodPost,
			body: `{"session_id":"web:test","text":"   "}`, want: http.StatusBadRequest},
		{name: "unusable session id", method: http.MethodPost,
			body: `{"session_id":"web:../etc","text":"hi"}`, want: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := newTestWebChannelWithSession(t, "web:test")
			if !tc.noQuote {
				ch.SetQuoteFn(func(context.Context, string, string) (string, error) {
					return "> ok", nil
				})
			}
			rw := httptest.NewRecorder()
			ch.handleQuote(rw, httptest.NewRequest(tc.method, "/api/quote", strings.NewReader(tc.body)))
			if rw.Code != tc.want {
				t.Errorf("status = %d, want %d; body=%s", rw.Code, tc.want, rw.Body.String())
			}
		})
	}
}
