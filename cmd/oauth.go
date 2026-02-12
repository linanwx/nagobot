package cmd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

const (
	oauthCallbackAddr = "localhost:1455"
	oauthCallbackPath = "/auth/callback"
	oauthTimeout      = 5 * time.Minute
	oauthHTTPTimeout  = 30 * time.Second
	oauthMaxBodySize  = 1 << 20 // 1MB
)

// oauthProvider defines OAuth endpoints for a provider.
type oauthProvider struct {
	Name     string
	AuthURL  string
	TokenURL string
	ClientID string
	Scopes   []string
}

var oauthProviders = map[string]oauthProvider{
	"openai": {
		Name:     "openai",
		AuthURL:  "https://auth.openai.com/oauth/authorize",
		TokenURL: "https://auth.openai.com/oauth/token",
		ClientID: "app_EMoamEEZ73f0CkXaXp7hrann",
		Scopes:   []string{"openid", "profile", "email", "offline_access", "model.request"},
	},
	"anthropic": {
		Name:     "anthropic",
		AuthURL:  "https://claude.ai/oauth/authorize",
		TokenURL: "https://console.anthropic.com/v1/oauth/token",
		ClientID: "", // not yet available
		Scopes:   []string{"user:inference", "user:profile"},
	},
}

var oauthCmd = &cobra.Command{
	Use:   "oauth",
	Short: "Manage OAuth authentication for LLM providers",
	Long: `Authenticate with OpenAI or Anthropic using OAuth.

Examples:
  nagobot oauth openai         # Login with OpenAI account
  nagobot oauth anthropic      # Login with Anthropic account (coming soon)
  nagobot oauth status         # Show current OAuth status
  nagobot oauth logout openai  # Remove OpenAI OAuth token`,
}

var oauthStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current OAuth token status",
	RunE:  runOAuthStatus,
}

var oauthLogoutCmd = &cobra.Command{
	Use:   "logout <provider>",
	Short: "Remove OAuth token for a provider",
	Args:  cobra.ExactArgs(1),
	RunE:  runOAuthLogout,
}

var oauthOpenAICmd = &cobra.Command{
	Use:   "openai",
	Short: "Login with OpenAI account",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runOAuthLogin("openai")
	},
}

var oauthAnthropicCmd = &cobra.Command{
	Use:   "anthropic",
	Short: "Login with Anthropic account (coming soon)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runOAuthLogin("anthropic")
	},
}

func init() {
	oauthCmd.AddCommand(oauthStatusCmd)
	oauthCmd.AddCommand(oauthLogoutCmd)
	oauthCmd.AddCommand(oauthOpenAICmd)
	oauthCmd.AddCommand(oauthAnthropicCmd)
	rootCmd.AddCommand(oauthCmd)

	// Wire OAuth token refresh into the provider factory.
	provider.SetOAuthRefresher(RefreshOAuthToken)
}

func runOAuthLogin(providerName string) error {
	prov, ok := oauthProviders[providerName]
	if !ok {
		return fmt.Errorf("unsupported OAuth provider: %s (supported: openai, anthropic)", providerName)
	}
	if prov.ClientID == "" {
		return fmt.Errorf("%s OAuth is not yet available (client_id not configured)", providerName)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Generate PKCE verifier + challenge.
	verifier, err := generateCodeVerifier()
	if err != nil {
		return fmt.Errorf("failed to generate code verifier: %w", err)
	}
	challenge := computeCodeChallenge(verifier)

	// Generate state for CSRF protection.
	state, err := generateRandomHex(16)
	if err != nil {
		return fmt.Errorf("failed to generate state: %w", err)
	}

	redirectURI := "http://" + oauthCallbackAddr + oauthCallbackPath

	// Build authorization URL.
	authURL := buildAuthURL(prov, redirectURI, challenge, state)

	// Start local callback server.
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	var callbackOnce sync.Once

	listener, err := net.Listen("tcp", oauthCallbackAddr)
	if err != nil {
		return fmt.Errorf("failed to start callback server on %s: %w", oauthCallbackAddr, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc(oauthCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		// Only handle the first valid callback.
		callbackOnce.Do(func() {
			if gotState := r.URL.Query().Get("state"); gotState != state {
				http.Error(w, "state mismatch", http.StatusForbidden)
				errCh <- fmt.Errorf("OAuth state mismatch")
				return
			}
			if errMsg := r.URL.Query().Get("error"); errMsg != "" {
				desc := r.URL.Query().Get("error_description")
				http.Error(w, "OAuth error: "+errMsg, http.StatusBadRequest)
				errCh <- fmt.Errorf("OAuth error: %s (%s)", errMsg, desc)
				return
			}
			code := r.URL.Query().Get("code")
			if code == "" {
				http.Error(w, "missing code", http.StatusBadRequest)
				errCh <- fmt.Errorf("no authorization code received")
				return
			}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><h2>Authorization successful!</h2><p>You can close this tab.</p></body></html>`)
			codeCh <- code
		})
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

	// Open browser.
	fmt.Printf("Opening browser for %s OAuth...\n", providerName)
	if err := openBrowser(authURL); err != nil {
		fmt.Println("Could not open browser automatically.")
		fmt.Println("Please open this URL in your browser:")
		fmt.Println()
		fmt.Println("  " + authURL)
		fmt.Println()
	}

	fmt.Println("Waiting for authorization...")

	// Wait for callback or timeout.
	var code string
	select {
	case code = <-codeCh:
		// success
	case err := <-errCh:
		return err
	case <-time.After(oauthTimeout):
		return fmt.Errorf("OAuth timeout: no callback received within %s", oauthTimeout)
	}

	// Exchange code for token.
	fmt.Println("Exchanging authorization code for token...")
	token, err := exchangeCodeForToken(prov, code, verifier, redirectURI)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	// Store token in config.
	cfg.SetOAuthToken(providerName, token)
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println()
	fmt.Printf("Successfully authenticated with %s!\n", providerName)
	if token.ExpiresAt > 0 {
		fmt.Printf("Token expires: %s\n", time.Unix(token.ExpiresAt, 0).Local().Format(time.RFC3339))
	}
	if token.RefreshToken != "" {
		fmt.Println("Refresh token saved (auto-refresh enabled).")
	}

	return nil
}

func runOAuthStatus(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	found := false
	for _, name := range []string{"openai", "anthropic"} {
		token := cfg.GetOAuthToken(name)
		if token == nil || token.AccessToken == "" {
			continue
		}
		found = true
		status := "active"
		if token.ExpiresAt > 0 {
			if time.Now().Unix() > token.ExpiresAt {
				if token.RefreshToken != "" {
					status = "expired (auto-refresh available)"
				} else {
					status = "expired"
				}
			} else {
				remaining := time.Until(time.Unix(token.ExpiresAt, 0))
				status = fmt.Sprintf("active (expires in %s)", remaining.Round(time.Minute))
			}
		}
		// Show only last 4 chars for safety.
		tokenPreview := "****"
		if len(token.AccessToken) > 4 {
			tokenPreview = "****" + token.AccessToken[len(token.AccessToken)-4:]
		}
		fmt.Printf("%s: %s [%s]\n", name, status, tokenPreview)
	}

	if !found {
		fmt.Println("No OAuth tokens configured.")
		fmt.Println("Run 'nagobot oauth openai' or 'nagobot oauth anthropic' to authenticate.")
	}
	return nil
}

func runOAuthLogout(_ *cobra.Command, args []string) error {
	providerName := strings.TrimSpace(args[0])
	if _, ok := oauthProviders[providerName]; !ok {
		return fmt.Errorf("unsupported provider: %s (supported: openai, anthropic)", providerName)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if token := cfg.GetOAuthToken(providerName); token == nil || token.AccessToken == "" {
		fmt.Printf("No OAuth token found for %s.\n", providerName)
		return nil
	}

	cfg.ClearOAuthToken(providerName)
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Printf("OAuth token for %s removed.\n", providerName)
	return nil
}

// --- PKCE helpers ---

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func computeCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func generateRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func buildAuthURL(prov oauthProvider, redirectURI, challenge, state string) string {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {prov.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(prov.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return prov.AuthURL + "?" + params.Encode()
}

// oauthTokenResponse is the JSON response from the token endpoint.
type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

var oauthHTTPClient = &http.Client{Timeout: oauthHTTPTimeout}

func exchangeCodeForToken(prov oauthProvider, code, verifier, redirectURI string) (*config.OAuthTokenConfig, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {prov.ClientID},
		"code_verifier": {verifier},
	}

	resp, err := oauthHTTPClient.PostForm(prov.TokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", prov.TokenURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, oauthMaxBodySize))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to extract error details from response body.
		var errResp oauthTokenResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("token endpoint HTTP %d: %s: %s", resp.StatusCode, errResp.Error, errResp.ErrorDesc)
		}
		return nil, fmt.Errorf("token endpoint returned HTTP %d", resp.StatusCode)
	}

	var tokenResp oauthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	if tokenResp.Error != "" {
		return nil, fmt.Errorf("%s: %s", tokenResp.Error, tokenResp.ErrorDesc)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access_token in response")
	}

	token := &config.OAuthTokenConfig{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
	}
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Unix() + tokenResp.ExpiresIn
	}
	return token, nil
}

// RefreshOAuthToken attempts to refresh an expired OAuth token.
// Returns the new access token on success, empty string on failure.
func RefreshOAuthToken(cfg *config.Config, providerName string) string {
	token := cfg.GetOAuthToken(providerName)
	if token == nil || token.RefreshToken == "" {
		return ""
	}

	prov, ok := oauthProviders[providerName]
	if !ok || prov.TokenURL == "" || prov.ClientID == "" {
		return ""
	}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {token.RefreshToken},
		"client_id":     {prov.ClientID},
	}

	resp, err := oauthHTTPClient.PostForm(prov.TokenURL, data)
	if err != nil {
		logger.Warn("oauth token refresh failed", "provider", providerName, "err", err)
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, oauthMaxBodySize))
	if err != nil {
		logger.Warn("oauth token refresh read error", "provider", providerName, "err", err)
		return ""
	}

	if resp.StatusCode != http.StatusOK {
		var errResp oauthTokenResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			logger.Warn("oauth token refresh failed", "provider", providerName, "status", resp.StatusCode, "error", errResp.Error)
		} else {
			logger.Warn("oauth token refresh HTTP error", "provider", providerName, "status", resp.StatusCode)
		}
		return ""
	}

	var tokenResp oauthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		logger.Warn("oauth token refresh parse error", "provider", providerName, "err", err)
		return ""
	}
	if tokenResp.AccessToken == "" {
		logger.Warn("oauth token refresh returned no access_token", "provider", providerName)
		return ""
	}

	newToken := &config.OAuthTokenConfig{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
	}
	if newToken.RefreshToken == "" {
		newToken.RefreshToken = token.RefreshToken // keep old refresh token
	}
	if tokenResp.ExpiresIn > 0 {
		newToken.ExpiresAt = time.Now().Unix() + tokenResp.ExpiresIn
	}

	cfg.SetOAuthToken(providerName, newToken)
	if err := cfg.Save(); err != nil {
		logger.Warn("oauth token refresh save error", "provider", providerName, "err", err)
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
