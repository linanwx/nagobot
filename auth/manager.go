package auth

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
)

const loginFlowTTL = 5 * time.Minute

// Manager is the web-auth facade: person registry, one-time login codes,
// device sessions, channel-identity dictionary, and WebAuthn ceremonies.
type Manager struct {
	persons    *personStore
	codes      *codeStore
	sessions   *sessionStore
	identities *identityStore
	limiter    *loginLimiter
	wa         *webauthn.WebAuthn

	disabled    bool
	exemptNets  []*net.IPNet
	publicURL   string
	listenAddr  string

	flowMu sync.Mutex
	flows  map[string]*loginFlow // in-flight passkey login ceremonies
}

type loginFlow struct {
	expires time.Time
	waData  *webauthn.SessionData
}

// NewManager builds the auth manager. systemDir is {workspace}/system.
func NewManager(systemDir string, cfg *config.Config) (*Manager, error) {
	persons, err := newPersonStore(systemDir)
	if err != nil {
		return nil, err
	}
	sessions, err := newSessionStore(systemDir)
	if err != nil {
		return nil, err
	}
	identities, err := newIdentityStore(systemDir)
	if err != nil {
		return nil, err
	}

	var ac *config.WebAuthConfig
	listenAddr := ""
	if cfg != nil {
		listenAddr = cfg.GetWebAddr()
		if cfg.Channels != nil && cfg.Channels.Web != nil {
			ac = cfg.Channels.Web.Auth
		}
	}
	if ac == nil {
		ac = &config.WebAuthConfig{}
	}

	m := &Manager{
		persons:    persons,
		codes:      newCodeStore(),
		sessions:   sessions,
		identities: identities,
		limiter:    newLoginLimiter(),
		disabled:   ac.Disabled,
		publicURL:  strings.TrimRight(strings.TrimSpace(ac.PublicURL), "/"),
		listenAddr: listenAddr,
		flows:      map[string]*loginFlow{},
	}

	for _, c := range ac.ExemptCIDRs {
		_, ipnet, err := net.ParseCIDR(strings.TrimSpace(c))
		if err != nil {
			return nil, fmt.Errorf("web auth exemptCidrs entry %q: %w", c, err)
		}
		m.exemptNets = append(m.exemptNets, ipnet)
	}

	rpid := strings.TrimSpace(ac.RPID)
	if rpid == "" {
		rpid = "localhost"
	}
	origins := ac.Origins
	if len(origins) == 0 {
		port := webPort(listenAddr)
		origins = []string{
			"http://localhost:" + port,
			"http://127.0.0.1:" + port,
		}
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpid,
		RPDisplayName: "nagobot",
		RPOrigins:     origins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationPreferred,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn config: %w", err)
	}
	m.wa = wa
	return m, nil
}

func webPort(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return port
	}
	return "18080"
}

// Enabled reports whether web auth is active.
func (m *Manager) Enabled() bool { return m != nil && !m.disabled }

// ExemptIP reports whether a request source IP skips auth. Only the direct
// RemoteAddr counts; forwarding headers are never consulted. There is no
// implicit exemption — not even loopback: a browser on the host machine must
// log in like any other. Deployments that want unauthenticated local tooling
// opt in explicitly with exemptCidrs: ["127.0.0.0/8"].
func (m *Manager) ExemptIP(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range m.exemptNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// MintLoginLink creates a one-time code and returns the full login URL.
func (m *Manager) MintLoginLink() (link string, expires time.Time) {
	code, expires := m.codes.mint()
	base := m.publicURL
	if base == "" {
		base = "http://localhost:" + webPort(m.listenAddr)
	}
	return base + "/?login_code=" + url.QueryEscape(code), expires
}

// RedeemCode consumes a one-time code and opens a setup session.
func (m *Manager) RedeemCode(code string) (setupToken string, ok bool) {
	return m.codes.redeem(code)
}

// SetupValid reports whether a setup token is live.
func (m *Manager) SetupValid(token string) bool {
	return m.codes.setup(token) != nil
}

// PersonSummary is the associate-flow listing entry.
type PersonSummary struct {
	ID         string   `json:"id"`
	Username   string   `json:"username"`
	Identities []string `json:"identities,omitempty"`
}

// Persons lists all persons (id, username, identities — no credentials).
func (m *Manager) Persons() []PersonSummary {
	list := m.persons.list()
	out := make([]PersonSummary, 0, len(list))
	for _, p := range list {
		out = append(out, PersonSummary{ID: p.ID, Username: p.Username, Identities: p.Identities})
	}
	return out
}

// Identities lists channel users seen by the system, for the binding UI.
func (m *Manager) Identities() []Identity {
	return m.identities.list()
}

// RecordIdentity notes a channel user (called by the dispatcher per message).
func (m *Manager) RecordIdentity(channelName, userID, displayName string) {
	if m == nil {
		return
	}
	if err := m.identities.record(channelName, userID, displayName); err != nil {
		logger.Warn("identity record failed", "channel", channelName, "user", userID, "err", err)
	}
}

// CreatePersonForSetup creates a new person and binds the setup session to it.
func (m *Manager) CreatePersonForSetup(setupToken, username string) (*Person, error) {
	if m.codes.setup(setupToken) == nil {
		return nil, fmt.Errorf("setup session expired")
	}
	p, err := m.persons.create(username)
	if err != nil {
		return nil, err
	}
	m.codes.mutateSetup(setupToken, func(s *setupSession) { s.personID = p.ID })
	return p, nil
}

// AssociateSetup binds the setup session to an existing person.
func (m *Manager) AssociateSetup(setupToken, personID string) (*Person, error) {
	p := m.persons.byID(personID)
	if p == nil {
		return nil, fmt.Errorf("person %s not found", personID)
	}
	if !m.codes.mutateSetup(setupToken, func(s *setupSession) { s.personID = p.ID }) {
		return nil, fmt.Errorf("setup session expired")
	}
	return p, nil
}

// AddIdentities binds channel identities to a person (second-confirmed in UI).
func (m *Manager) AddIdentities(personID string, identities []string) error {
	for _, id := range identities {
		if err := m.persons.addIdentity(personID, id); err != nil {
			return err
		}
	}
	return nil
}

// BeginRegistration starts a passkey registration for the setup session's
// bound person.
func (m *Manager) BeginRegistration(setupToken string) (*protocol.CredentialCreation, error) {
	s := m.codes.setup(setupToken)
	if s == nil {
		return nil, fmt.Errorf("setup session expired")
	}
	if s.personID == "" {
		return nil, fmt.Errorf("setup session has no person bound yet")
	}
	p := m.persons.byID(s.personID)
	if p == nil {
		return nil, fmt.Errorf("person %s not found", s.personID)
	}
	options, waData, err := m.wa.BeginRegistration(waUser{p})
	if err != nil {
		return nil, fmt.Errorf("begin registration: %w", err)
	}
	m.codes.mutateSetup(setupToken, func(s *setupSession) { s.waData = waData })
	return options, nil
}

// FinishRegistration completes the ceremony, stores the credential, closes
// the setup session, and issues a device session token.
func (m *Manager) FinishRegistration(setupToken string, r *http.Request) (sessionToken string, person *Person, err error) {
	s := m.codes.setup(setupToken)
	if s == nil || s.waData == nil || s.personID == "" {
		return "", nil, fmt.Errorf("setup session expired or registration not begun")
	}
	p := m.persons.byID(s.personID)
	if p == nil {
		return "", nil, fmt.Errorf("person %s not found", s.personID)
	}
	cred, err := m.wa.FinishRegistration(waUser{p}, *s.waData, r)
	if err != nil {
		return "", nil, fmt.Errorf("finish registration: %w", err)
	}
	if err := m.persons.addCredential(p.ID, *cred); err != nil {
		return "", nil, err
	}
	m.codes.dropSetup(setupToken)
	token, err := m.sessions.mint(p.ID, r.UserAgent())
	if err != nil {
		return "", nil, err
	}
	return token, p, nil
}

// BeginLogin starts a usernameless (discoverable-credential) passkey login.
func (m *Manager) BeginLogin() (flowID string, options *protocol.CredentialAssertion, err error) {
	options, waData, err := m.wa.BeginDiscoverableLogin()
	if err != nil {
		return "", nil, fmt.Errorf("begin login: %w", err)
	}
	flowID = randomToken(16)
	m.flowMu.Lock()
	defer m.flowMu.Unlock()
	now := time.Now()
	for k, f := range m.flows {
		if now.After(f.expires) {
			delete(m.flows, k)
		}
	}
	m.flows[flowID] = &loginFlow{expires: now.Add(loginFlowTTL), waData: waData}
	return flowID, options, nil
}

// FinishLogin completes the assertion and issues a device session token.
func (m *Manager) FinishLogin(flowID string, r *http.Request) (sessionToken string, person *Person, err error) {
	m.flowMu.Lock()
	f := m.flows[flowID]
	delete(m.flows, flowID)
	m.flowMu.Unlock()
	if f == nil || time.Now().After(f.expires) {
		return "", nil, fmt.Errorf("login flow expired")
	}

	var matched *Person
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		p := m.persons.byID(string(userHandle))
		if p == nil {
			return nil, fmt.Errorf("unknown user handle")
		}
		matched = p
		return waUser{p}, nil
	}
	cred, err := m.wa.FinishDiscoverableLogin(handler, *f.waData, r)
	if err != nil {
		return "", nil, fmt.Errorf("finish login: %w", err)
	}
	if cred.Authenticator.CloneWarning {
		logger.Warn("passkey clone warning", "person", matched.ID)
	}
	if err := m.persons.updateCredential(matched.ID, *cred); err != nil {
		logger.Warn("credential update failed", "person", matched.ID, "err", err)
	}
	token, err := m.sessions.mint(matched.ID, r.UserAgent())
	if err != nil {
		return "", nil, err
	}
	return token, matched, nil
}

// SessionTTL is the device-session idle window; the web layer uses it as the
// cookie MaxAge and re-issues the cookie on activity so both slide together.
func (m *Manager) SessionTTL() time.Duration { return deviceSessionTTL }

// ValidateSession resolves a device-session cookie token to a person.
func (m *Manager) ValidateSession(token string) (*Person, bool) {
	personID, ok := m.sessions.validate(token)
	if !ok {
		return nil, false
	}
	p := m.persons.byID(personID)
	if p == nil {
		return nil, false
	}
	return p, true
}

// RevokeSession logs out a device-session token.
func (m *Manager) RevokeSession(token string) {
	m.sessions.revoke(token)
}
