package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/provider"
)

func TestManagerSaveAndReloadRoundTrip(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	mgr, err := NewManager(sessionsDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	createdAt := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	input := &Session{
		Key:       "chat:user-1",
		Messages:  []provider.Message{provider.UserMessage("hello")},
		CreatedAt: createdAt,
	}
	if err := mgr.Save(input); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	path := mgr.PathForKey("chat:user-1")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file should exist at %s: %v", path, err)
	}

	got, err := mgr.Reload("chat:user-1")
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if got.Key != "chat:user-1" {
		t.Fatalf("Reload().Key = %q, want %q", got.Key, "chat:user-1")
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != "user" || got.Messages[0].Content != "hello" {
		t.Fatalf("Reload().Messages = %+v, want one user message", got.Messages)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("Reload() timestamps should not be zero: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
}

func TestManagerPathForKeySanitizesAndDefaults(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	mgr, err := NewManager(sessionsDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	defaultPath := mgr.PathForKey("   ")
	if !strings.HasSuffix(defaultPath, filepath.Join("sessions", "cli", "session.json")) {
		t.Fatalf("default path = %q, want suffix %q", defaultPath, filepath.Join("sessions", "cli", "session.json"))
	}

	sanitizedPath := mgr.PathForKey(" parent : ../bad?? : child ")
	if strings.Contains(sanitizedPath, "..") {
		t.Fatalf("sanitized path should not contain '..': %q", sanitizedPath)
	}
	if !strings.HasSuffix(sanitizedPath, filepath.Join("sessions", "parent", "bad", "child", "session.json")) {
		t.Fatalf("sanitized path = %q, want suffix %q", sanitizedPath, filepath.Join("sessions", "parent", "bad", "child", "session.json"))
	}
}

func TestSaveAutoAssignsIDsAndTimestamps(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	mgr, err := NewManager(sessionsDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	s := &Session{
		Key: "tg:42",
		Messages: []provider.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
			{Role: "user", Content: "bye"},
		},
		CreatedAt: time.Now(),
	}
	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	for i, m := range s.Messages {
		if m.ID == "" {
			t.Fatalf("Messages[%d].ID should be assigned after Save, got empty", i)
		}
		if m.Timestamp.IsZero() {
			t.Fatalf("Messages[%d].Timestamp should be assigned after Save, got zero", i)
		}
		if !strings.HasPrefix(m.ID, "tg:42:") {
			t.Fatalf("Messages[%d].ID = %q, want prefix %q", i, m.ID, "tg:42:")
		}
	}

	// IDs should be ordered (same prefix, ascending sequence).
	for i := 1; i < len(s.Messages); i++ {
		if s.Messages[i].ID <= s.Messages[i-1].ID {
			t.Fatalf("Messages[%d].ID (%q) should be > Messages[%d].ID (%q)",
				i, s.Messages[i].ID, i-1, s.Messages[i-1].ID)
		}
	}
}

func TestSavePreservesExistingIDs(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	mgr, err := NewManager(sessionsDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	existingID := "tg:42:1700000000000:000"
	existingTS := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := &Session{
		Key: "tg:42",
		Messages: []provider.Message{
			{Role: "user", Content: "old", ID: existingID, Timestamp: existingTS},
			{Role: "user", Content: "new"},
		},
		CreatedAt: time.Now(),
	}
	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if s.Messages[0].ID != existingID {
		t.Fatalf("Messages[0].ID changed from %q to %q", existingID, s.Messages[0].ID)
	}
	if !s.Messages[0].Timestamp.Equal(existingTS) {
		t.Fatalf("Messages[0].Timestamp changed from %v to %v", existingTS, s.Messages[0].Timestamp)
	}
	if s.Messages[1].ID == "" {
		t.Fatal("Messages[1].ID should have been assigned")
	}
}

func TestLoadFromDiskBackfillsLegacyIDs(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	mgr, err := NewManager(sessionsDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Save a session, then strip IDs/timestamps from the file to simulate legacy data.
	s := &Session{
		Key:       "legacy:user",
		Messages:  []provider.Message{{Role: "user", Content: "old message"}},
		CreatedAt: time.Now(),
	}
	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Re-write file without ID/Timestamp fields (simulate legacy format).
	path := mgr.PathForKey("legacy:user")
	legacyJSON := `{
		"key": "legacy:user",
		"messages": [
			{"role": "user", "content": "old message"},
			{"role": "assistant", "content": "old reply"}
		],
		"created_at": "2025-01-01T00:00:00Z",
		"updated_at": "2025-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(path, []byte(legacyJSON), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	loaded, err := mgr.Reload("legacy:user")
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	for i, m := range loaded.Messages {
		if m.ID == "" {
			t.Fatalf("loaded Messages[%d].ID should be backfilled, got empty", i)
		}
		if m.Timestamp.IsZero() {
			t.Fatalf("loaded Messages[%d].Timestamp should be backfilled, got zero", i)
		}
		if !strings.HasPrefix(m.ID, "legacy:user:") {
			t.Fatalf("loaded Messages[%d].ID = %q, want prefix %q", i, m.ID, "legacy:user:")
		}
	}
}

func TestManagerGetUsesCache(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	mgr, err := NewManager(sessionsDir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	first, err := mgr.Get("cache:key")
	if err != nil {
		t.Fatalf("Get(first) error = %v", err)
	}
	second, err := mgr.Get("cache:key")
	if err != nil {
		t.Fatalf("Get(second) error = %v", err)
	}
	if first != second {
		t.Fatalf("Get() should return cached pointer for same key")
	}
}
