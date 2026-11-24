package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/linanwx/nagobot/config"
)

// TestOAuthProviderReadsTokenPerRequest pins the kingsley 2026-11-24
// incident: a coder child built its openai-oauth provider with ~50s left on
// the access token, the token expired mid-turn, and its 10th request failed
// with 401 token_expired, killing the turn. A provider lives one turn and a
// token one hour, so the bearer must be resolved (refresh included) on every
// request, not once at construction.
func TestOAuthProviderReadsTokenPerRequest(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	now := time.Now().Unix()
	cfg := &config.Config{Thread: config.ThreadConfig{Provider: "openai-oauth", ModelType: "gpt-6.1-sol"}}
	cfg.Providers.OpenAIOAuth = &config.OAuthTokenConfig{
		AccessToken: "token-at-turn-start", RefreshToken: "r", ClientID: "oaiapp_x", ExpiresAt: now + 3600,
	}
	factory, err := NewFactory(func() *config.Config { return cfg })
	if err != nil {
		t.Fatal(err)
	}
	p, err := factory.Create("openai-oauth", "gpt-6.1-sol")
	if err != nil {
		t.Fatal(err)
	}
	op := p.(*OpenAIProvider)
	op.baseURL = server.URL

	prev := oauthRefresher
	defer func() { oauthRefresher = prev }()
	oauthRefresher = func(c *config.Config, name string) string {
		c.Providers.OpenAIOAuth.AccessToken = "token-after-refresh"
		c.Providers.OpenAIOAuth.ExpiresAt = time.Now().Unix() + 3600
		return "token-after-refresh"
	}

	send := func() {
		resp, err := op.doRequest(context.Background(), server.URL, []byte("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	send()
	// The token expires mid-turn: the next request on the SAME provider must
	// refresh rather than resend the dead token.
	cfg.Providers.OpenAIOAuth.ExpiresAt = now - 1
	send()

	want := []string{"Bearer token-at-turn-start", "Bearer token-after-refresh"}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("Authorization headers = %q, want %q", seen, want)
	}
}

// TestOAuthProviderFailsLoudWithoutToken: when the refresh fails mid-turn the
// request must fail with a sign-in hint, not go out with an empty bearer.
func TestOAuthProviderFailsLoudWithoutToken(t *testing.T) {
	op := newOpenAIProvider("stale", "", "gpt-6.1-sol", "", 0, 0)
	op.setTokenFn(func() string { return "" })
	if _, err := op.doRequest(context.Background(), "http://127.0.0.1:1", []byte("{}")); err == nil {
		t.Fatal("request with no token went out instead of failing")
	}
}
