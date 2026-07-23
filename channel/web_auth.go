package channel

import (
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/linanwx/nagobot/auth"
	"github.com/linanwx/nagobot/logger"
)

const (
	sessionCookieName = "nagobot_session"
	setupCookieName   = "nagobot_setup"
)

// authorize resolves the request to an access decision. Exempt IPs
// (configured CIDRs only — loopback has no implicit pass) and disabled auth
// pass without a person; otherwise a valid device-session cookie is required.
func (w *WebChannel) authorize(r *http.Request) (person *auth.Person, allowed bool) {
	if w.authMgr == nil || !w.authMgr.Enabled() {
		return nil, true
	}
	if w.authMgr.ExemptIP(r.RemoteAddr) {
		return nil, true
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		if p, ok := w.authMgr.ValidateSession(c.Value); ok {
			return p, true
		}
	}
	return nil, false
}

// protected wraps an API handler with the auth check. On a cookie-authenticated
// request it also re-issues the session cookie with a fresh MaxAge, so the
// cookie's lifetime slides together with the server-side session's sliding
// LastSeen — an active browser never hits a hard cookie expiry.
func (w *WebChannel) protected(h http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		person, ok := w.authorize(r)
		if !ok {
			http.Error(rw, "authentication required", http.StatusUnauthorized)
			return
		}
		if person != nil {
			// person != nil means the session cookie validated (exempt/auth-off
			// requests carry nil) — safe to re-issue what the browser sent.
			if c, err := r.Cookie(sessionCookieName); err == nil {
				setCookie(rw, sessionCookieName, c.Value, w.authMgr.SessionTTL())
			}
		}
		h(rw, r)
	}
}

func writeJSON(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(v)
}

func setCookie(rw http.ResponseWriter, name, value string, maxAge time.Duration) {
	http.SetCookie(rw, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(rw http.ResponseWriter, name string) {
	http.SetCookie(rw, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// --- GET /api/auth/me ---
// Public: tells the frontend whether to show the login gate.
func (w *WebChannel) handleAuthMe(rw http.ResponseWriter, r *http.Request) {
	type meResponse struct {
		AuthEnabled   bool     `json:"auth_enabled"`
		Exempt        bool     `json:"exempt"`
		Authenticated bool     `json:"authenticated"`
		PersonID      string   `json:"person_id,omitempty"`
		Username      string   `json:"username,omitempty"`
		Identities    []string `json:"identities,omitempty"`
		// SetupLive reports a still-valid setup cookie: the browser redeemed a
		// login link (30 min) but hasn't finished registering a credential.
		// The frontend uses it to resume the setup wizard instead of showing
		// "link invalid" when a spent link is reopened in the same browser.
		SetupLive bool `json:"setup_live,omitempty"`
	}
	resp := meResponse{AuthEnabled: w.authMgr != nil && w.authMgr.Enabled()}
	if !resp.AuthEnabled {
		resp.Exempt = true
		resp.Authenticated = true
		writeJSON(rw, http.StatusOK, resp)
		return
	}
	if w.authMgr.ExemptIP(r.RemoteAddr) {
		resp.Exempt = true
		resp.Authenticated = true
		writeJSON(rw, http.StatusOK, resp)
		return
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		if p, ok := w.authMgr.ValidateSession(c.Value); ok {
			resp.Authenticated = true
			resp.PersonID = p.ID
			resp.Username = p.Username
			resp.Identities = p.Identities
		}
	}
	if !resp.Authenticated {
		resp.SetupLive = w.setupToken(r) != ""
	}
	writeJSON(rw, http.StatusOK, resp)
}

// --- POST /api/auth/redeem {code} ---
// Public: consumes a one-time login-link code, opens a setup session.
func (w *WebChannel) handleAuthRedeem(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.Code == "" {
		http.Error(rw, "missing code", http.StatusBadRequest)
		return
	}
	setupToken, ok := w.authMgr.RedeemCode(p.Code)
	if !ok {
		// Expired, unknown, or already used — indistinguishable by design.
		http.Error(rw, "login link invalid or expired", http.StatusGone)
		return
	}
	setCookie(rw, setupCookieName, setupToken, 30*time.Minute)
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}

// setupToken extracts a live setup session token from the request, or "".
func (w *WebChannel) setupToken(r *http.Request) string {
	c, err := r.Cookie(setupCookieName)
	if err != nil || !w.authMgr.SetupValid(c.Value) {
		return ""
	}
	return c.Value
}

// --- GET /api/auth/context ---
// Setup-scoped: the choices for the create/associate screen.
func (w *WebChannel) handleAuthContext(rw http.ResponseWriter, r *http.Request) {
	if w.setupToken(r) == "" {
		http.Error(rw, "no live setup session", http.StatusUnauthorized)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"persons":    w.authMgr.Persons(),
		"identities": w.authMgr.Identities(),
	})
}

// --- POST /api/auth/setup {mode, username?, person_id?, identities?} ---
// Setup-scoped: create a new person or associate an existing one, plus
// optional channel-identity bindings (the UI double-confirms these).
func (w *WebChannel) handleAuthSetup(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := w.setupToken(r)
	if token == "" {
		http.Error(rw, "no live setup session", http.StatusUnauthorized)
		return
	}
	var p struct {
		Mode       string   `json:"mode"` // "create" | "associate"
		Username   string   `json:"username"`
		PersonID   string   `json:"person_id"`
		Identities []string `json:"identities"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(rw, "bad request body", http.StatusBadRequest)
		return
	}
	var person *auth.Person
	var err error
	switch p.Mode {
	case "create":
		person, err = w.authMgr.CreatePersonForSetup(token, p.Username)
	case "associate":
		person, err = w.authMgr.AssociateSetup(token, p.PersonID)
	default:
		http.Error(rw, "mode must be create or associate", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if len(p.Identities) > 0 {
		if err := w.authMgr.AddIdentities(person.ID, p.Identities); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
	}
	writeJSON(rw, http.StatusOK, map[string]string{"person_id": person.ID, "username": person.Username})
}

// --- POST /api/auth/passkey/register/begin ---
func (w *WebChannel) handleRegisterBegin(rw http.ResponseWriter, r *http.Request) {
	token := w.setupToken(r)
	if token == "" {
		http.Error(rw, "no live setup session", http.StatusUnauthorized)
		return
	}
	options, err := w.authMgr.BeginRegistration(token)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(rw, http.StatusOK, options)
}

// --- POST /api/auth/passkey/register/finish (body = attestation response) ---
func (w *WebChannel) handleRegisterFinish(rw http.ResponseWriter, r *http.Request) {
	token := w.setupToken(r)
	if token == "" {
		http.Error(rw, "no live setup session", http.StatusUnauthorized)
		return
	}
	sessionToken, person, err := w.authMgr.FinishRegistration(token, r)
	if err != nil {
		logger.Warn("passkey registration failed", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	clearCookie(rw, setupCookieName)
	setCookie(rw, sessionCookieName, sessionToken, w.authMgr.SessionTTL())
	writeJSON(rw, http.StatusOK, map[string]string{"username": person.Username})
}

// remoteIP extracts the host part of RemoteAddr for the login rate limiter.
// Deliberately never consults forwarding headers, matching ExemptIP.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- POST /api/auth/password/set {password} ---
// Setup-scoped: the password counterpart of passkey registration, for
// devices with no passkey provider (GMS-less Android). Stores the bcrypt
// hash, closes the setup session, and leaves the browser logged in.
func (w *WebChannel) handlePasswordSet(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := w.setupToken(r)
	if token == "" {
		http.Error(rw, "no live setup session", http.StatusUnauthorized)
		return
	}
	var p struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(rw, "bad request body", http.StatusBadRequest)
		return
	}
	sessionToken, person, err := w.authMgr.SetPasswordForSetup(token, p.Password, r.UserAgent())
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	clearCookie(rw, setupCookieName)
	setCookie(rw, sessionCookieName, sessionToken, w.authMgr.SessionTTL())
	writeJSON(rw, http.StatusOK, map[string]string{"username": person.Username})
}

// --- POST /api/auth/password/login {username, password} ---
// Public: rate-limited (per username+IP) with a uniform error, see
// Manager.PasswordLogin.
func (w *WebChannel) handlePasswordLogin(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var p struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.Username == "" || p.Password == "" {
		http.Error(rw, "missing username or password", http.StatusBadRequest)
		return
	}
	sessionToken, person, err := w.authMgr.PasswordLogin(p.Username, p.Password, remoteIP(r), r.UserAgent())
	if err != nil {
		logger.Warn("password login failed", "username", p.Username, "ip", remoteIP(r))
		http.Error(rw, err.Error(), http.StatusUnauthorized)
		return
	}
	setCookie(rw, sessionCookieName, sessionToken, w.authMgr.SessionTTL())
	writeJSON(rw, http.StatusOK, map[string]string{"username": person.Username})
}

// --- POST /api/auth/passkey/login/begin ---
func (w *WebChannel) handleLoginBegin(rw http.ResponseWriter, r *http.Request) {
	flowID, options, err := w.authMgr.BeginLogin()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"flow_id": flowID, "options": options})
}

// --- POST /api/auth/passkey/login/finish?flow={id} (body = assertion) ---
func (w *WebChannel) handleLoginFinish(rw http.ResponseWriter, r *http.Request) {
	flowID := r.URL.Query().Get("flow")
	if flowID == "" {
		http.Error(rw, "missing flow id", http.StatusBadRequest)
		return
	}
	sessionToken, person, err := w.authMgr.FinishLogin(flowID, r)
	if err != nil {
		logger.Warn("passkey login failed", "err", err)
		http.Error(rw, err.Error(), http.StatusUnauthorized)
		return
	}
	setCookie(rw, sessionCookieName, sessionToken, w.authMgr.SessionTTL())
	writeJSON(rw, http.StatusOK, map[string]string{"username": person.Username})
}

// --- POST /api/auth/logout ---
func (w *WebChannel) handleLogout(rw http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		w.authMgr.RevokeSession(c.Value)
	}
	clearCookie(rw, sessionCookieName)
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}
