package auth

import (
	"fmt"
	"strings"
	"time"
)

// sendKeyPrefix marks a send key so a pasted value is recognizable as one,
// and so a device-session token can never be mistaken for it.
const sendKeyPrefix = "nsk_"

// SendKey is a bearer credential for POST /api/send: a device that cannot run
// a login ceremony (an Apple Watch shortcut) posts a message as its person
// into exactly one session. Only the SHA-256 of the key is stored; the raw
// key is shown once, at mint time.
//
// The key is bound to a session rather than naming one per request so a
// leaked key can do at most one thing: speak into that session as that
// person. It cannot read history or reach any other session.
type SendKey struct {
	Hash      string    `json:"hash"`
	Session   string    `json:"session"`
	CreatedAt time.Time `json:"created_at"`
}

// addSendKey mints a key for a person, bound to sessionKey.
func (s *personStore) addSendKey(personID, sessionKey string) (string, error) {
	raw := sendKeyPrefix + randomToken(24)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.persons {
		if p.ID == personID {
			p.SendKeys = append(p.SendKeys, SendKey{Hash: hashToken(raw), Session: sessionKey, CreatedAt: time.Now()})
			if err := s.saveLocked(); err != nil {
				p.SendKeys = p.SendKeys[:len(p.SendKeys)-1]
				return "", fmt.Errorf("save persons store: %w", err)
			}
			return raw, nil
		}
	}
	return "", fmt.Errorf("person %s not found", personID)
}

// revokeSendKeys drops a person's keys bound to sessionKey, or all of them
// when sessionKey is empty. Returns how many were removed.
func (s *personStore) revokeSendKeys(personID, sessionKey string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.persons {
		if p.ID != personID {
			continue
		}
		kept := p.SendKeys[:0:0]
		for _, k := range p.SendKeys {
			if sessionKey == "" || k.Session == sessionKey {
				continue
			}
			kept = append(kept, k)
		}
		removed := len(p.SendKeys) - len(kept)
		if removed == 0 {
			return 0, nil
		}
		prev := p.SendKeys
		p.SendKeys = kept
		if err := s.saveLocked(); err != nil {
			p.SendKeys = prev
			return 0, fmt.Errorf("save persons store: %w", err)
		}
		return removed, nil
	}
	return 0, fmt.Errorf("person %s not found", personID)
}

// bySendKey resolves a raw send key to its person and bound session.
func (s *personStore) bySendKey(raw string) (*Person, string, bool) {
	if !strings.HasPrefix(raw, sendKeyPrefix) {
		return nil, "", false
	}
	h := hashToken(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.persons {
		for _, k := range p.SendKeys {
			if k.Hash == h {
				return p, k.Session, true
			}
		}
	}
	return nil, "", false
}

// MintSendKey creates a send key for the named person, bound to sessionKey.
// The raw key is returned once and never stored.
func (m *Manager) MintSendKey(username, sessionKey string) (string, error) {
	p := m.persons.byUsername(username)
	if p == nil {
		return "", fmt.Errorf("user %q not found", username)
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return "", fmt.Errorf("session is empty")
	}
	return m.persons.addSendKey(p.ID, sessionKey)
}

// RevokeSendKeys removes the named person's send keys for sessionKey, or all
// of them when sessionKey is empty.
func (m *Manager) RevokeSendKeys(username, sessionKey string) (int, error) {
	p := m.persons.byUsername(username)
	if p == nil {
		return 0, fmt.Errorf("user %q not found", username)
	}
	return m.persons.revokeSendKeys(p.ID, strings.TrimSpace(sessionKey))
}

// ResolveSendKey maps a raw send key to the person it belongs to and the
// session it may post into.
func (m *Manager) ResolveSendKey(raw string) (*Person, string, bool) {
	if m == nil {
		return nil, "", false
	}
	return m.persons.bySendKey(strings.TrimSpace(raw))
}
