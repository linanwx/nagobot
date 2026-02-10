package thread

import (
	"testing"
)

func TestNewThread(t *testing.T) {
	mgr := NewManager(nil)
	th, err := mgr.NewThread("test:1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if th == nil {
		t.Fatal("thread should be initialized")
	}
	if th.Agent == nil {
		t.Fatal("agent should be initialized (fallback)")
	}
}

func TestNewThreadWithSession(t *testing.T) {
	mgr := NewManager(nil)
	th, err := mgr.NewThread("chat:user", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if th == nil {
		t.Fatal("thread should be initialized")
	}
	if th.sessionKey != "chat:user" {
		t.Fatalf("sessionKey mismatch: got %q", th.sessionKey)
	}
}

func TestManagerNewThreadReuses(t *testing.T) {
	mgr := NewManager(&ThreadConfig{})

	first, err := mgr.NewThread("room:1", "a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := mgr.NewThread("room:1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first != second {
		t.Fatal("expected thread reuse")
	}
}

func TestThreadSet(t *testing.T) {
	mgr := NewManager(nil)
	th, err := mgr.NewThread("test:set", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	th.Set("TASK", "do something")
	if th.Agent == nil {
		t.Fatal("agent should be initialized")
	}
}
