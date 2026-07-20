package auth

import (
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	loginCodeTTL    = 30 * time.Minute
	setupSessionTTL = 30 * time.Minute
)

// loginCode is a one-time bootstrap credential minted by the CLI and
// delivered as a link. Codes live in memory only: a daemon restart voids
// outstanding links, which is acceptable for a 30-minute credential.
type loginCode struct {
	expires time.Time
}

// setupSession is the short-lived state between redeeming a code and
// finishing passkey registration. It is bound to a person once the browser
// chooses "create" or "associate".
type setupSession struct {
	expires  time.Time
	personID string
	waData   *webauthn.SessionData // in-flight registration ceremony
}

type codeStore struct {
	mu     sync.Mutex
	codes  map[string]*loginCode
	setups map[string]*setupSession
}

func newCodeStore() *codeStore {
	return &codeStore{
		codes:  map[string]*loginCode{},
		setups: map[string]*setupSession{},
	}
}

func (c *codeStore) pruneLocked(now time.Time) {
	for k, v := range c.codes {
		if now.After(v.expires) {
			delete(c.codes, k)
		}
	}
	for k, v := range c.setups {
		if now.After(v.expires) {
			delete(c.setups, k)
		}
	}
}

// mint creates a fresh one-time login code.
func (c *codeStore) mint() (code string, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.pruneLocked(now)
	code = randomToken(16)
	expires = now.Add(loginCodeTTL)
	c.codes[code] = &loginCode{expires: expires}
	return code, expires
}

// redeem consumes a code (single use) and opens a setup session.
// Returns ("", false) when the code is unknown, expired, or already used.
func (c *codeStore) redeem(code string) (setupToken string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.pruneLocked(now)
	lc := c.codes[code]
	if lc == nil {
		return "", false
	}
	delete(c.codes, code)
	setupToken = randomToken(16)
	c.setups[setupToken] = &setupSession{expires: now.Add(setupSessionTTL)}
	return setupToken, true
}

// setup returns the live setup session for a token, or nil.
func (c *codeStore) setup(token string) *setupSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(time.Now())
	return c.setups[token]
}

// mutateSetup runs fn on the live setup session under the store lock.
// Returns false when the token is unknown or expired.
func (c *codeStore) mutateSetup(token string, fn func(*setupSession)) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(time.Now())
	s := c.setups[token]
	if s == nil {
		return false
	}
	fn(s)
	return true
}

// dropSetup ends a setup session (after successful registration).
func (c *codeStore) dropSetup(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.setups, token)
}
