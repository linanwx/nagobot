package thread

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/linanwx/nagobot/session"
)

func newClarifyThread(t *testing.T, content string) *Thread {
	t.Helper()
	store, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewManager: %v", err)
	}
	mgr := NewManager(&ThreadConfig{Sessions: store})
	dir := filepath.Dir(store.PathForKey("cli"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(dir, "clarify.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	th, err := mgr.NewThread("cli", "")
	if err != nil {
		t.Fatalf("NewThread: %v", err)
	}
	return th
}

// TestClarifySection_InjectsSmallListWhole: a list under the cap is injected
// verbatim with no overlong warning, and a missing or blank file adds nothing.
func TestClarifySection_InjectsSmallListWhole(t *testing.T) {
	const list = "- Which bank holds the deposit? — decides the AIP paperwork\n- Is the viewing on Saturday or Sunday? — the cron depends on it"
	got := newClarifyThread(t, list).buildClarifySection()
	if !strings.Contains(got, "type: to_clarify") || !strings.HasSuffix(got, list) {
		t.Fatalf("clarify section did not carry the list whole:\n%s", got)
	}
	if strings.Contains(got, promptHeaderClarifyOverlong.Text()) {
		t.Errorf("small list was flagged overlong")
	}
	for _, empty := range []string{"", "  \n\n "} {
		if s := newClarifyThread(t, empty).buildClarifySection(); s != "" {
			t.Errorf("content %q: want no section, got:\n%s", empty, s)
		}
	}
}

// TestClarifySection_CapsAndFlagsOverlongList: the file rides in every turn, so
// an overlong one is cut at a line boundary under the cap, and the header says
// it was cut rather than dropping the tail silently.
func TestClarifySection_CapsAndFlagsOverlongList(t *testing.T) {
	line := "- " + strings.Repeat("问", 290) + " — why"
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, line)
	}
	got := newClarifyThread(t, strings.Join(lines, "\n")).buildClarifySection()
	if !strings.Contains(got, promptHeaderClarifyOverlong.Text()) {
		t.Fatalf("overlong list not flagged:\n%.300s", got)
	}
	body := got[strings.Index(got, "\n---\n\n")+len("\n---\n\n"):]
	if n := utf8.RuneCountInString(body); n > clarifyMaxRunes {
		t.Errorf("injected body is %d runes, cap %d", n, clarifyMaxRunes)
	}
	for _, l := range strings.Split(body, "\n") {
		if l != line {
			t.Errorf("body was cut mid-line: %.40q", l)
		}
	}
}
