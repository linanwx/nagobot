package thread

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linanwx/nagobot/session"
)

// TestParentSessionKey covers the sibling/child markers parentSessionKey strips
// so section lookups resolve to the root session. The :threads: case is the
// subagent (e.g. delegated search) child; it must reduce to the root the same
// way :prethink does.
func TestParentSessionKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"cli", "cli"},
		{"telegram:42", "telegram:42"},
		{"cli:prethink", "cli"},
		{"cli:fork:summarize", "cli"},
		{"cli:threads:search-abc", "cli"},
		{"discord:123:threads:foo", "discord:123"},
		{"wecom:group:abc:threads:search-x", "wecom:group:abc"},
		{"cli:threads:a:threads:b", "cli"}, // nested → before the first :threads:
	}
	for _, c := range cases {
		if got := parentSessionKey(c.in); got != c.want {
			t.Errorf("parentSessionKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSectionDir_SubagentInheritsParentMemory verifies a delegated subagent
// thread ({parent}:threads:{taskID}, e.g. the search agent) resolves its
// user_memory_section and memory_index_section to the ROOT session's USER.md and
// memory/ — not its own empty child dir. This is what makes search land on the
// main session's memory files, like pre-think.
func TestSectionDir_SubagentInheritsParentMemory(t *testing.T) {
	store, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewManager: %v", err)
	}
	mgr := NewManager(&ThreadConfig{Sessions: store})

	parentKey := "cli"
	parentDir := filepath.Dir(store.PathForKey(parentKey))
	if err := os.MkdirAll(filepath.Join(parentDir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	const userContent = "## User Preferences\n\nLikes concise answers."
	if err := os.WriteFile(filepath.Join(parentDir, "USER.md"), []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "memory", "2026-06-01.md"),
		[]byte("---\nsummary: talked about widgets\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	childKey := parentKey + session.ThreadsSessionInfix + "search-abc"
	child, err := mgr.NewThread(childKey, "")
	if err != nil {
		t.Fatalf("NewThread: %v", err)
	}

	if user := child.buildUserSection(); !strings.Contains(user, "Likes concise answers.") {
		t.Errorf("subagent buildUserSection did not inherit parent USER.md:\n%s", user)
	}
	if mem := child.buildMemoryIndexSection(); !strings.Contains(mem, "talked about widgets") {
		t.Errorf("subagent buildMemoryIndexSection did not inherit parent memory/:\n%s", mem)
	}
}
