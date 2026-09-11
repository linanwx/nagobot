package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/monitor"
	"github.com/linanwx/nagobot/provider"
)

// storedOAuthToken plants an openai-oauth credential in an isolated config dir
// and returns the config the checkers are built from.
func storedOAuthToken(t *testing.T, token *config.OAuthTokenConfig) *config.Config {
	t.Helper()

	config.SetConfigDir(t.TempDir())
	t.Cleanup(func() { config.SetConfigDir("") })

	cfg := config.DefaultConfig()
	cfg.SetOAuthToken("openai-oauth", token)
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	return cfg
}

// openAIQuotaChecker pulls the OpenAI checker out of the wired set, so the test
// exercises the closure the daemon actually polls with rather than a
// reconstruction of it.
func openAIQuotaChecker(t *testing.T, cfg *config.Config) *monitor.OpenAIQuota {
	t.Helper()

	for _, c := range buildBalanceCheckers(cfg, t.TempDir()) {
		if q, ok := c.(*monitor.OpenAIQuota); ok {
			return q
		}
	}
	t.Fatal("no OpenAIQuota checker in the wired set")
	return nil
}

// stubOAuthRefresher installs a refresher for the duration of the test and puts
// the real one back afterwards, since it is a process-global set from cmd.init.
func stubOAuthRefresher(t *testing.T, fn func(*config.Config, string) string) {
	t.Helper()

	provider.SetOAuthRefresher(fn)
	t.Cleanup(func() { provider.SetOAuthRefresher(RefreshOAuthToken) })
}

// The bug this pins: the probe used to read cfg.GetOAuthToken().AccessToken
// straight, and refresh fires only when a turn builds the provider. On a
// deployment that routes nothing to openai-oauth the stored token expires and
// stays expired, so the balance probe sent a dead JWT to the usage endpoint
// every five minutes and reported the 401 as a credential problem — six
// consecutive days of it on kingsley, with inference entirely unaffected
// because no turn ever asked for the provider. Resolving through the same
// accessor the inference path uses is what makes the probe self-heal.
func TestOpenAIQuotaCredsRefreshAnExpiredStoredToken(t *testing.T) {
	cfg := storedOAuthToken(t, &config.OAuthTokenConfig{
		AccessToken:  "stale-token",
		RefreshToken: "refresh-me",
		ExpiresAt:    time.Now().Add(-6 * 24 * time.Hour).Unix(),
		AccountID:    "acct-old",
	})

	refreshes := 0
	stubOAuthRefresher(t, func(c *config.Config, name string) string {
		refreshes++
		// Mirror the real refresher: rewrite the stored credential in place,
		// account id included, and hand back the new access token.
		c.SetOAuthToken(name, &config.OAuthTokenConfig{
			AccessToken:  "fresh-token",
			RefreshToken: "refresh-me",
			ExpiresAt:    time.Now().Add(10 * 24 * time.Hour).Unix(),
			AccountID:    "acct-new",
		})
		return "fresh-token"
	})

	token, accountID, err := openAIQuotaChecker(t, cfg).CredsFn()
	if err != nil {
		t.Fatalf("unexpected credential error: %v", err)
	}
	if token != "fresh-token" {
		t.Fatalf("probe would have used %q; a stored token that expired must be refreshed first", token)
	}
	if refreshes != 1 {
		t.Fatalf("refresher ran %d time(s), want exactly 1", refreshes)
	}
	// A refresh response may carry a new id_token, so the account id has to be
	// re-read after the refresh rather than captured before it.
	if accountID != "acct-new" {
		t.Fatalf("account id %q is the pre-refresh one", accountID)
	}
}

// A token that is still valid must go out untouched: refreshing on every poll
// would burn the refresh grant for nothing.
func TestOpenAIQuotaCredsLeaveAValidTokenAlone(t *testing.T) {
	cfg := storedOAuthToken(t, &config.OAuthTokenConfig{
		AccessToken:  "live-token",
		RefreshToken: "refresh-me",
		ExpiresAt:    time.Now().Add(6 * time.Hour).Unix(),
		AccountID:    "acct-1",
	})
	stubOAuthRefresher(t, func(*config.Config, string) string {
		t.Fatal("refreshed a token that had not expired")
		return ""
	})

	token, accountID, err := openAIQuotaChecker(t, cfg).CredsFn()
	if err != nil {
		t.Fatalf("unexpected credential error: %v", err)
	}
	if token != "live-token" || accountID != "acct-1" {
		t.Fatalf("got (%q, %q), want the stored credential", token, accountID)
	}
}

// A credential that exists but cannot be renewed must surface as an error, not
// as absent credentials: the two prescribe different remedies, and reporting
// "no OAuth token configured" for a token sitting in config is the wrong-remedy
// alert this whole checker exists to avoid.
func TestOpenAIQuotaCredsReportAFailedRefresh(t *testing.T) {
	expired := time.Now().Add(-30 * time.Hour)

	for name, refresher := range map[string]func(*config.Config, string) string{
		"refresh rejected": func(*config.Config, string) string { return "" },
		"no refresh token": nil,
	} {
		t.Run(name, func(t *testing.T) {
			stored := &config.OAuthTokenConfig{
				AccessToken:  "stale-token",
				RefreshToken: "refresh-me",
				ExpiresAt:    expired.Unix(),
			}
			if refresher == nil {
				// Nothing to refresh with — the credential needs a re-login.
				stored.RefreshToken = ""
				refresher = func(*config.Config, string) string {
					t.Fatal("attempted a refresh with no refresh token")
					return ""
				}
			}
			cfg := storedOAuthToken(t, stored)
			stubOAuthRefresher(t, refresher)

			token, _, err := openAIQuotaChecker(t, cfg).CredsFn()
			if token != "" {
				t.Fatalf("got token %q, want none", token)
			}
			if err == nil {
				t.Fatal("a configured-but-unusable credential reported as absent")
			}
			if !strings.Contains(err.Error(), "auth login") {
				t.Fatalf("error names no remedy: %v", err)
			}
			// The expiry instant is the one fact that separates "expired six
			// days ago and nobody noticed" from "just expired".
			if !strings.Contains(err.Error(), expired.UTC().Format("2006-01-02")) {
				t.Fatalf("error does not say when the token expired: %v", err)
			}
		})
	}
}

// Available gates every poll and must answer from stored config, but it still
// has to track the credential rather than a startup snapshot.
func TestOpenAIQuotaAvailabilityFollowsStoredConfig(t *testing.T) {
	cfg := storedOAuthToken(t, &config.OAuthTokenConfig{AccessToken: "live-token"})
	q := openAIQuotaChecker(t, cfg)

	if !q.Available() {
		t.Fatal("expected available with a token stored")
	}

	cfg.ClearOAuthToken("openai-oauth")
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if q.Available() {
		t.Fatal("expected unavailable once the stored token is gone")
	}
}
