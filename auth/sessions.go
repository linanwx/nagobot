package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	// deviceSessionTTL is a SLIDING window: validate() refreshes LastSeen on
	// every hit, so an active browser stays logged in indefinitely and only
	// 30 days of idleness expires the session. The cookie is re-issued on
	// activity by the web layer so it slides in step.
	deviceSessionTTL = 30 * 24 * time.Hour
	// lastSeenGranularity bounds how often a session's LastSeen update is
	// persisted; in-memory it is always current.
	lastSeenGranularity = time.Hour
)

// deviceSession is one logged-in browser. The cookie holds the raw token;
// only its SHA-256 is stored.
type deviceSession struct {
	TokenHash string    `json:"token_hash"`
	PersonID  string    `json:"person_id"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	UserAgent string    `json:"user_agent,omitempty"`
}

type sessionsFile struct {
	Sessions []*deviceSession `json:"sessions"`
}

type sessionStore struct {
	mu       sync.Mutex
	path     string
	sessions map[string]*deviceSession // keyed by TokenHash
	dirty    map[string]time.Time      // TokenHash → LastSeen at last persist
}

func newSessionStore(systemDir string) (*sessionStore, error) {
	s := &sessionStore{
		path:     filepath.Join(systemDir, "web_sessions.json"),
		sessions: map[string]*deviceSession{},
		dirty:    map[string]time.Time{},
	}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read web sessions store: %w", err)
	}
	var f sessionsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	now := time.Now()
	for _, ds := range f.Sessions {
		if now.Sub(ds.LastSeen) < deviceSessionTTL {
			s.sessions[ds.TokenHash] = ds
			s.dirty[ds.TokenHash] = ds.LastSeen
		}
	}
	return s, nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *sessionStore) saveLocked() error {
	list := make([]*deviceSession, 0, len(s.sessions))
	for _, ds := range s.sessions {
		list = append(list, ds)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	data, err := json.MarshalIndent(sessionsFile{Sessions: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	for h, ds := range s.sessions {
		s.dirty[h] = ds.LastSeen
	}
	return nil
}

// mint creates a device session for a person and returns the raw token.
func (s *sessionStore) mint(personID, userAgent string) (string, error) {
	token := randomToken(32)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[hashToken(token)] = &deviceSession{
		TokenHash: hashToken(token),
		PersonID:  personID,
		CreatedAt: now,
		LastSeen:  now,
		UserAgent: userAgent,
	}
	if err := s.saveLocked(); err != nil {
		return "", fmt.Errorf("save web sessions store: %w", err)
	}
	return token, nil
}

// validate resolves a raw cookie token to a person ID. LastSeen is updated
// in memory on every hit but persisted at most once per lastSeenGranularity.
func (s *sessionStore) validate(token string) (personID string, ok bool) {
	if token == "" {
		return "", false
	}
	h := hashToken(token)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ds := s.sessions[h]
	if ds == nil {
		return "", false
	}
	if now.Sub(ds.LastSeen) >= deviceSessionTTL {
		delete(s.sessions, h)
		_ = s.saveLocked()
		return "", false
	}
	ds.LastSeen = now
	if now.Sub(s.dirty[h]) >= lastSeenGranularity {
		_ = s.saveLocked()
	}
	return ds.PersonID, true
}

// revoke deletes the session for a raw token (logout).
func (s *sessionStore) revoke(token string) {
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[h]; exists {
		delete(s.sessions, h)
		_ = s.saveLocked()
	}
}
