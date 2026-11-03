package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

const (
	oauthCallbackAddr = "127.0.0.1:1455"
	oauthCallbackPath = "/auth/callback"
	oauthTimeout      = 10 * time.Minute
	oauthHTTPTimeout  = 30 * time.Second
	oauthMaxBodySize  = 1 << 20 // 1MB
)

// oauthConfig describes one "Sign in with ChatGPT" (SIWC) token-sharing
// integration. There is no fixed client id: the first sign-in presents
// dynamic_agent_client plus an agent name and a stable host id, and OpenAI
// issues this install its own client id (oaiapp_...) in the callback. That
// issued id, not a hard-coded one, is what every token exchange and refresh
// presents. See developers.openai.com/siwc/token-sharing-open-source/sign-in.
type oauthConfig struct {
	AuthURL       string
	TokenURL      string
	Resource      string
	Scopes        []string
	RequiredScope string // must be granted, or the token cannot call the model API
	AgentName     string
}

var openaiOAuthConfig = &oauthConfig{
	AuthURL:       "https://auth.openai.com/api/accounts/authorize",
	TokenURL:      "https://auth.openai.com/api/accounts/oauth/token",
	Resource:      "https://api.openai.com/v1",
	Scopes:        []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"},
	RequiredScope: "chatgpt.tokens.use.direct",
	AgentName:     "nagobot",
}

// authProviders is the registry of providers that support OAuth login. Only
// openai-oauth: the "openai" provider authenticates with an API key alone.
var authProviders = map[string]*oauthConfig{
	"openai-oauth": openaiOAuthConfig,
}

// authProviderAliases lets `auth logout openai` keep working.
var authProviderAliases = map[string]string{
	"openai": "openai-oauth",
}

var authCmd = &cobra.Command{
	Use:     "auth",
	Aliases: []string{"oauth"},
	Short:   "Manage OAuth authentication for LLM providers",
	Long: `Authenticate with LLM providers via OAuth.

Examples:
  nagobot auth openai         # Sign in with ChatGPT (provider openai-oauth)
  nagobot auth status         # Show current token status
  nagobot auth logout openai  # Remove token for a provider`,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current auth token status",
	RunE:  runAuthStatus,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout <provider>",
	Short: "Remove auth token for a provider",
	Args:  cobra.ExactArgs(1),
	RunE:  runAuthLogout,
}

var authOpenAICmd = &cobra.Command{
	Use:   "openai",
	Short: "Sign in with ChatGPT for the openai-oauth provider",
	Long: `Sign in with ChatGPT for the openai-oauth provider.

A browser opens on OpenAI's sign-in page. On a machine without a browser, open
the printed URL anywhere; after approving, the browser lands on a 127.0.0.1
page that fails to load. Copy that page's full address and paste it here.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runOAuthLogin("openai-oauth")
	},
}

func init() {
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authOpenAICmd)
	rootCmd.AddCommand(authCmd)

	// Wire OAuth token refresh into the provider factory.
	provider.SetOAuthRefresher(RefreshOAuthToken)
}

// oauthCallback is what the authorize redirect carries back.
type oauthCallback struct {
	code     string
	clientID string
	scope    string
}

func runOAuthLogin(providerName string) error {
	oa, ok := authProviders[providerName]
	if !ok {
		return fmt.Errorf("unsupported OAuth provider: %s", providerName)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// The device id must stay stable across re-logins so OpenAI sees the same
	// agent host rather than registering a new one each time.
	deviceID := ""
	if prev := cfg.GetOAuthToken(providerName); prev != nil {
		deviceID = prev.DeviceID
	}
	if deviceID == "" {
		deviceID = uuid.NewString()
	}

	verifier, err := randomURLToken(32)
	if err != nil {
		return fmt.Errorf("failed to generate code verifier: %w", err)
	}
	state, err := randomURLToken(32)
	if err != nil {
		return fmt.Errorf("failed to generate state: %w", err)
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	redirectURI := "http://" + oauthCallbackAddr + oauthCallbackPath
	authURL := buildAuthURL(oa, redirectURI, computeCodeChallenge(verifier), state, nonce, deviceID)

	resultCh := make(chan oauthCallback, 1)
	errCh := make(chan error, 1)
	var once sync.Once
	deliver := func(cb oauthCallback, err error) {
		once.Do(func() {
			if err != nil {
				errCh <- err
				return
			}
			resultCh <- cb
		})
	}

	// Local callback server. A busy port is not fatal: the paste path below
	// still completes the login.
	listener, listenErr := net.Listen("tcp", oauthCallbackAddr)
	if listenErr == nil {
		mux := http.NewServeMux()
		mux.HandleFunc(oauthCallbackPath, func(w http.ResponseWriter, r *http.Request) {
			cb, err := parseOAuthCallback(r.URL.Query(), state)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				deliver(oauthCallback{}, err)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><h2>Authorization successful!</h2><p>You can close this tab.</p></body></html>`)
			deliver(cb, nil)
		})
		server := &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go server.Serve(listener)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server.Shutdown(ctx)
		}()
	} else {
		fmt.Printf("Note: could not listen on %s (%v); use the paste option below.\n", oauthCallbackAddr, listenErr)
	}

	// Paste fallback for headless machines: the browser that approved the
	// login ends on an unreachable 127.0.0.1 page whose address carries the
	// code. Malformed lines are reported and the prompt waits for another.
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			u, err := url.Parse(line)
			if err != nil || (u.Query().Get("code") == "" && u.Query().Get("error") == "") {
				fmt.Println("That does not look like the redirect address; paste the full URL from the browser's address bar.")
				continue
			}
			cb, err := parseOAuthCallback(u.Query(), state)
			deliver(cb, err)
			return
		}
	}()

	fmt.Println("Sign in with ChatGPT: open this URL in a browser.")
	fmt.Println()
	fmt.Println("  " + authURL)
	fmt.Println()
	_ = openBrowser(authURL)
	fmt.Println("Waiting for authorization... (on a machine without a browser, paste the final")
	fmt.Println("127.0.0.1 address from the browser's address bar here and press Enter)")

	var cb oauthCallback
	select {
	case cb = <-resultCh:
	case err := <-errCh:
		return err
	case <-time.After(oauthTimeout):
		return fmt.Errorf("OAuth timeout: no callback received within %s", oauthTimeout)
	}

	fmt.Println("Exchanging authorization code for token...")
	token, err := exchangeCodeForToken(oa, cb, verifier, redirectURI)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}
	token.DeviceID = deviceID

	cfg.SetOAuthToken(providerName, token)
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println()
	fmt.Printf("Successfully signed in for %s.\n", providerName)
	if token.RefreshToken != "" {
		fmt.Println("Refresh token saved (auto-refresh enabled).")
	}
	return nil
}

// parseOAuthCallback validates the authorize redirect's query and extracts the
// code and the client id OpenAI issued to this install.
func parseOAuthCallback(q url.Values, wantState string) (oauthCallback, error) {
	if q.Get("state") != wantState {
		return oauthCallback{}, fmt.Errorf("OAuth state mismatch")
	}
	if errMsg := q.Get("error"); errMsg != "" {
		return oauthCallback{}, fmt.Errorf("OAuth error: %s (%s)", errMsg, q.Get("error_description"))
	}
	cb := oauthCallback{code: q.Get("code"), clientID: q.Get("client_id"), scope: q.Get("scope")}
	if cb.code == "" {
		return oauthCallback{}, fmt.Errorf("no authorization code received")
	}
	if cb.clientID == "" {
		return oauthCallback{}, fmt.Errorf("no client_id in the callback: OpenAI did not issue a client for this install")
	}
	return cb, nil
}

func resolveAuthProviderName(name string) string {
	if canonical, ok := authProviderAliases[name]; ok {
		return canonical
	}
	return name
}

// allAuthProviderNames returns a sorted list of all provider names that support OAuth login.
func allAuthProviderNames() []string {
	names := make([]string, 0, len(authProviders))
	for name := range authProviders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func runAuthStatus(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	found := false
	for _, name := range allAuthProviderNames() {
		token := cfg.GetOAuthToken(name)
		if token == nil || token.AccessToken == "" {
			continue
		}
		found = true
		status := "active"
		switch {
		case token.ClientID == "":
			status = "legacy token, sign in again with 'nagobot auth openai'"
		case token.ExpiresAt > 0 && time.Now().Unix() > token.ExpiresAt:
			if token.RefreshToken != "" {
				status = "expired (auto-refresh available)"
			} else {
				status = "expired"
			}
		case token.ExpiresAt > 0:
			remaining := time.Until(time.Unix(token.ExpiresAt, 0))
			status = fmt.Sprintf("active (expires in %s)", remaining.Round(time.Minute))
		}
		// Show only last 4 chars for safety.
		tokenPreview := "****"
		if len(token.AccessToken) > 4 {
			tokenPreview = "****" + token.AccessToken[len(token.AccessToken)-4:]
		}
		fmt.Printf("%s: %s [%s]\n", name, status, tokenPreview)
	}

	if !found {
		fmt.Println("No auth tokens configured.")
		fmt.Println("Run 'nagobot auth openai' to authenticate.")
	}
	return nil
}

func runAuthLogout(_ *cobra.Command, args []string) error {
	providerName := resolveAuthProviderName(strings.TrimSpace(args[0]))
	if _, ok := authProviders[providerName]; !ok {
		return fmt.Errorf("unsupported provider: %s (supported: %s)", providerName, strings.Join(allAuthProviderNames(), ", "))
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if token := cfg.GetOAuthToken(providerName); token == nil || token.AccessToken == "" {
		fmt.Printf("No token found for %s.\n", providerName)
		return nil
	}

	cfg.ClearOAuthToken(providerName)
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Printf("Token for %s removed.\n", providerName)
	return nil
}

// --- PKCE helpers ---

// randomURLToken returns n random bytes, base64url-encoded without padding.
// Used for the PKCE verifier, state and nonce.
func randomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func computeCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func buildAuthURL(oa *oauthConfig, redirectURI, challenge, state, nonce, deviceID string) string {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {"dynamic_agent_client"},
		"agent_name_hint":       {oa.AgentName},
		"ext_agent_host_id":     {"urn:uuid:" + deviceID},
		"redirect_uri":          {redirectURI},
		"resource":              {oa.Resource},
		"scope":                 {strings.Join(oa.Scopes, " ")},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return oa.AuthURL + "?" + params.Encode()
}

// oauthTokenResponse is the JSON response from the token endpoint.
type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

var oauthHTTPClient = &http.Client{Timeout: oauthHTTPTimeout}

// postTokenForm sends one form request to the token endpoint and decodes a
// successful response.
func postTokenForm(oa *oauthConfig, data url.Values) (*oauthTokenResponse, error) {
	resp, err := oauthHTTPClient.PostForm(oa.TokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", oa.TokenURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, oauthMaxBodySize))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var tokenResp oauthTokenResponse
	parseErr := json.Unmarshal(body, &tokenResp)
	if resp.StatusCode != http.StatusOK {
		if parseErr == nil && tokenResp.Error != "" {
			return nil, fmt.Errorf("token endpoint HTTP %d: %s: %s", resp.StatusCode, tokenResp.Error, tokenResp.ErrorDesc)
		}
		return nil, fmt.Errorf("token endpoint returned HTTP %d", resp.StatusCode)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", parseErr)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("%s: %s", tokenResp.Error, tokenResp.ErrorDesc)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access_token in response")
	}
	return &tokenResp, nil
}

func exchangeCodeForToken(oa *oauthConfig, cb oauthCallback, verifier, redirectURI string) (*config.OAuthTokenConfig, error) {
	tokenResp, err := postTokenForm(oa, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {cb.clientID},
		"code":          {cb.code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
		"resource":      {oa.Resource},
	})
	if err != nil {
		return nil, err
	}

	// A token without the direct-use scope authenticates fine and then fails
	// every model call, so refuse it here where the cause is still legible.
	granted := tokenResp.Scope
	if granted == "" {
		granted = cb.scope
	}
	if !slices.Contains(strings.Fields(granted), oa.RequiredScope) {
		return nil, fmt.Errorf("sign-in did not grant the %q scope (granted: %q); this ChatGPT account cannot share its subscription with nagobot", oa.RequiredScope, granted)
	}

	token := &config.OAuthTokenConfig{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ClientID:     cb.clientID,
	}
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Unix() + tokenResp.ExpiresIn
	}
	return token, nil
}

// RefreshOAuthToken attempts to refresh an expired OAuth token.
// Returns the new access token on success, empty string on failure.
//
// Refresh tokens rotate: the old one stops working once this call succeeds, so
// a failure to persist the new one strands the login until the next sign-in.
// That is why the save error is logged at Error, not Warn.
func RefreshOAuthToken(cfg *config.Config, providerName string) string {
	token := cfg.GetOAuthToken(providerName)
	if token == nil || token.RefreshToken == "" || token.ClientID == "" {
		return ""
	}

	oa, ok := authProviders[providerName]
	if !ok {
		return ""
	}

	tokenResp, err := postTokenForm(oa, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {token.ClientID},
		"refresh_token": {token.RefreshToken},
		"resource":      {oa.Resource},
	})
	if err != nil {
		logger.Warn("oauth token refresh failed", "provider", providerName, "err", err)
		return ""
	}

	newToken := &config.OAuthTokenConfig{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ClientID:     token.ClientID,
		DeviceID:     token.DeviceID,
	}
	if newToken.RefreshToken == "" {
		newToken.RefreshToken = token.RefreshToken // keep old refresh token
	}
	if tokenResp.ExpiresIn > 0 {
		newToken.ExpiresAt = time.Now().Unix() + tokenResp.ExpiresIn
	}

	cfg.SetOAuthToken(providerName, newToken)
	if err := cfg.Save(); err != nil {
		logger.Error("oauth token refresh save error: the rotated refresh token was not persisted", "provider", providerName, "err", err)
	}
	return newToken.AccessToken
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}
