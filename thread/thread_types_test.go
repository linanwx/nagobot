package thread

import (
	"testing"
)

func TestNewThread(t *testing.T) {
	mgr := NewManager(nil)
	th := mgr.NewThread("test:1", "")
	if th == nil {
		t.Fatal("thread should be initialized")
	}
	if th.Agent == nil {
		t.Fatal("agent should be initialized (fallback)")
	}
}

func TestNewThreadWithSession(t *testing.T) {
	mgr := NewManager(nil)
	th := mgr.NewThread("chat:user", "")
	if th == nil {
		t.Fatal("thread should be initialized")
	}
	if th.sessionKey != "chat:user" {
		t.Fatalf("sessionKey mismatch: got %q", th.sessionKey)
	}
}

func TestManagerNewThreadReuses(t *testing.T) {
	mgr := NewManager(&ThreadConfig{})

	first := mgr.NewThread("room:1", "a")
	second := mgr.NewThread("room:1", "")
	if first != second {
		t.Fatal("expected thread reuse")
	}
}

func TestThreadSet(t *testing.T) {
	mgr := NewManager(nil)
	th := mgr.NewThread("test:set", "")
	th.Set("TASK", "do something")
	if th.Agent == nil {
		t.Fatal("agent should be initialized")
	}
}
