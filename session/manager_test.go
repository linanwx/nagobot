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
	workspace := t.TempDir()
	mgr, err := NewManager(workspace)
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
	workspace := t.TempDir()
	mgr, err := NewManager(workspace)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	defaultPath := mgr.PathForKey("   ")
	if !strings.HasSuffix(defaultPath, filepath.Join("sessions", "main", "session.json")) {
		t.Fatalf("default path = %q, want suffix %q", defaultPath, filepath.Join("sessions", "main", "session.json"))
	}

	sanitizedPath := mgr.PathForKey(" parent : ../bad?? : child ")
	if strings.Contains(sanitizedPath, "..") {
		t.Fatalf("sanitized path should not contain '..': %q", sanitizedPath)
	}
	if !strings.HasSuffix(sanitizedPath, filepath.Join("sessions", "parent", "bad", "child", "session.json")) {
		t.Fatalf("sanitized path = %q, want suffix %q", sanitizedPath, filepath.Join("sessions", "parent", "bad", "child", "session.json"))
	}
}

func TestManagerGetUsesCache(t *testing.T) {
	workspace := t.TempDir()
	mgr, err := NewManager(workspace)
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
