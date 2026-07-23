package auth

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	minPasswordLen = 8
	maxPasswordLen = 128 // bcrypt truncates at 72 bytes; reject absurd input earlier

	// Login rate limit: a username+IP pair gets loginMaxFailures wrong
	// attempts per loginFailureWindow, then is locked out for the remainder
	// of the window. Successful login clears the counter.
	loginMaxFailures   = 10
	loginFailureWindow = 15 * time.Minute
)

// loginLimiter is an in-memory failure counter keyed by username+IP. State
// is deliberately not persisted: a daemon restart forgiving outstanding
// lockouts is acceptable, matching the in-memory login codes.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string]*failureWindow
}

type failureWindow struct {
	start time.Time
	count int
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{failures: map[string]*failureWindow{}}
}

func limiterKey(username, ip string) string {
	return strings.ToLower(strings.TrimSpace(username)) + "|" + ip
}

// blocked reports whether this username+IP is currently locked out.
func (l *loginLimiter) blocked(username, ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, w := range l.failures { // opportunistic prune
		if now.Sub(w.start) >= loginFailureWindow {
			delete(l.failures, k)
		}
	}
	w := l.failures[limiterKey(username, ip)]
	return w != nil && w.count >= loginMaxFailures
}

// recordFailure counts one wrong attempt.
func (l *loginLimiter) recordFailure(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := limiterKey(username, ip)
	now := time.Now()
	w := l.failures[k]
	if w == nil || now.Sub(w.start) >= loginFailureWindow {
		l.failures[k] = &failureWindow{start: now, count: 1}
		return
	}
	w.count++
}

// clear resets the counter after a successful login.
func (l *loginLimiter) clear(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, limiterKey(username, ip))
}

// dummyHash is a valid bcrypt hash of a random string, generated once and
// used to burn comparable time on unknown-user login attempts so they are
// not distinguishable from wrong-password attempts by response latency.
var (
	dummyHashOnce sync.Once
	dummyHash     []byte
)

func getDummyHash() []byte {
	dummyHashOnce.Do(func() {
		h, err := bcrypt.GenerateFromPassword([]byte(randomToken(16)), bcrypt.DefaultCost)
		if err != nil {
			panic(fmt.Sprintf("bcrypt dummy hash: %v", err))
		}
		dummyHash = h
	})
	return dummyHash
}

func validatePassword(password string) error {
	if len(password) < minPasswordLen {
		return fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(password) > maxPasswordLen {
		return fmt.Errorf("password too long (max %d characters)", maxPasswordLen)
	}
	return nil
}

// SetPasswordForSetup stores a password for the setup session's bound person,
// closes the setup session, and issues a device session — the password
// counterpart of FinishRegistration, for devices with no passkey provider.
func (m *Manager) SetPasswordForSetup(setupToken, password, userAgent string) (sessionToken string, person *Person, err error) {
	s := m.codes.setup(setupToken)
	if s == nil || s.personID == "" {
		return "", nil, fmt.Errorf("setup session expired or no person bound")
	}
	if err := validatePassword(password); err != nil {
		return "", nil, err
	}
	p := m.persons.byID(s.personID)
	if p == nil {
		return "", nil, fmt.Errorf("person %s not found", s.personID)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", nil, fmt.Errorf("hash password: %w", err)
	}
	if err := m.persons.setPassword(p.ID, string(hash)); err != nil {
		return "", nil, err
	}
	m.codes.dropSetup(setupToken)
	token, err := m.sessions.mint(p.ID, userAgent)
	if err != nil {
		return "", nil, err
	}
	return token, p, nil
}

// PasswordLogin verifies username+password and issues a device session.
// The error message is uniform for unknown user / no password set / wrong
// password so usernames cannot be enumerated; ip feeds the rate limiter.
func (m *Manager) PasswordLogin(username, password, ip, userAgent string) (sessionToken string, person *Person, err error) {
	uniform := fmt.Errorf("invalid username or password")
	if m.limiter.blocked(username, ip) {
		return "", nil, fmt.Errorf("too many failed attempts — try again later")
	}
	p := m.persons.byUsername(username)
	if p == nil || p.PasswordHash == "" {
		_ = bcrypt.CompareHashAndPassword(getDummyHash(), []byte(password))
		m.limiter.recordFailure(username, ip)
		return "", nil, uniform
	}
	if err := bcrypt.CompareHashAndPassword([]byte(p.PasswordHash), []byte(password)); err != nil {
		m.limiter.recordFailure(username, ip)
		return "", nil, uniform
	}
	m.limiter.clear(username, ip)
	token, err := m.sessions.mint(p.ID, userAgent)
	if err != nil {
		return "", nil, err
	}
	return token, p, nil
}
