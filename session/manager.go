package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/provider"
)

// Session represents a conversation session.
type Session struct {
	Key       string             `json:"key"`
	Messages  []provider.Message `json:"messages"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// Manager manages conversation sessions.
type Manager struct {
	sessionsDir string
	cache       map[string]*Session
	mu          sync.RWMutex
}

// NewManager creates a new session manager rooted at the given sessions directory.
func NewManager(sessionsDir string) (*Manager, error) {
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return nil, err
	}
	return &Manager{
		sessionsDir: sessionsDir,
		cache:       make(map[string]*Session),
	}, nil
}

// Get returns a session by key, creating one if it doesn't exist.
func (m *Manager) Get(key string) (*Session, error) {
	key = normalizeSessionKey(key)

	m.mu.RLock()
	if s, ok := m.cache[key]; ok {
		m.mu.RUnlock()
		return s, nil
	}
	m.mu.RUnlock()

	s, err := m.loadFromDisk(key)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if cached, ok := m.cache[key]; ok {
		m.mu.Unlock()
		return cached, nil
	}
	m.cache[key] = s
	m.mu.Unlock()
	return s, nil
}

// Reload forces loading session state from disk and refreshes cache.
func (m *Manager) Reload(key string) (*Session, error) {
	key = normalizeSessionKey(key)

	s, err := m.loadFromDisk(key)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.cache[key] = s
	m.mu.Unlock()
	return s, nil
}

// Save saves a session to disk.
// Orphaned tool messages (no preceding tool_calls) are automatically removed.
func (m *Manager) Save(s *Session) error {
	s.Key = normalizeSessionKey(s.Key)
	s.UpdatedAt = time.Now()
	s.Messages = provider.SanitizeMessages(s.Messages)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := m.sessionPath(s.Key)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (m *Manager) sessionPath(key string) string {
	key = normalizeSessionKey(key)
	parts := strings.Split(key, ":")
	cleanParts := make([]string, 0, len(parts)+1)
	for _, p := range parts {
		segment := sanitizePathSegment(p)
		if segment == "" {
			continue
		}
		cleanParts = append(cleanParts, segment)
	}
	if len(cleanParts) == 0 {
		cleanParts = append(cleanParts, "cli")
	}
	cleanParts = append(cleanParts, "session.json")
	return filepath.Join(append([]string{m.sessionsDir}, cleanParts...)...)
}

// PathForKey returns the on-disk session file path for a session key.
func (m *Manager) PathForKey(key string) string {
	return m.sessionPath(key)
}

func (m *Manager) loadFromDisk(key string) (*Session, error) {
	key = normalizeSessionKey(key)

	path := m.sessionPath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			now := time.Now()
			return &Session{
				Key:       key,
				Messages:  []provider.Message{},
				CreatedAt: now,
				UpdatedAt: now,
			}, nil
		}
		return nil, err
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.Key) == "" {
		s.Key = key
	}
	if s.Messages == nil {
		s.Messages = []provider.Message{}
	}
	s.Messages = provider.SanitizeMessages(s.Messages)
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.CreatedAt
	}
	return &s, nil
}

func normalizeSessionKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "cli"
	}
	return key
}

func sanitizePathSegment(segment string) string {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(segment))
	lastUnderscore := false
	for _, r := range segment {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "_"
	}
	return out
}
