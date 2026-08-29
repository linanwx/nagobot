package channel

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePromptFixture lays out the two halves of the editable prompt set: the
// fixed top-level files and a sections directory.
func writePromptFixture(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	sys := filepath.Join(ws, "system")
	sections := filepath.Join(sys, "sections")
	if err := os.MkdirAll(sections, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sys, "GLOBAL.md"), []byte("persona"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Deliberately out of alphabetical order relative to priority, so the test
	// distinguishes "sorted by priority" from "sorted by file name".
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(sections, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("zeta.md", "---\nname: context\npriority: 100\n---\n# Context\n")
	write("acting-safely.md", "---\nname: acting-safely\npriority: 201\n---\n# Acting safely\n")
	// No frontmatter at all — must still be listed, under its file name.
	write("bare.md", "no frontmatter here\n")
	return ws
}

// The editable prompt set is DERIVED from the sections directory, not from a
// hand-maintained list. A list has to be updated by whoever adds a section, and
// a missed row is invisible in the worst way: the section is live in every
// agent's prompt and absent from the editor.
func TestPromptFileSpecsAreDerivedFromTheSectionsDirectory(t *testing.T) {
	ch := &WebChannel{workspace: writePromptFixture(t)}
	var names, labels []string
	for _, s := range promptFileSpecs(ch.workspace) {
		names = append(names, s.Name)
		labels = append(labels, s.Label)
	}
	// Sections follow the fixed top-level files, ordered by (priority, name) —
	// the order Build renders them, not the order ReadDir returns them.
	// bare.md carries no frontmatter, so its priority is 0 and it sorts first.
	want := []string{
		"GLOBAL.md", "world_knowledge.md", "people_knowledge.md",
		"sections/bare.md",          // priority 0 (no frontmatter)
		"sections/zeta.md",          // priority 100, name "context"
		"sections/acting-safely.md", // priority 201
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("derived set/order wrong:\ngot:  %v\nwant: %v", names, want)
	}
	// The label comes from the frontmatter name, rendered for display; a file
	// with no frontmatter falls back to its file name rather than vanishing.
	for i, want := range []string{"Global persona", "World knowledge", "People knowledge", "Bare", "Context", "Acting safely"} {
		if labels[i] != want {
			t.Errorf("label[%d] = %q, want %q", i, labels[i], want)
		}
	}
}

// The derived set is also the path check. Membership, not shape: every legal
// name came from a ReadDir of the sections directory, so a traversal string can
// never be one of them.
func TestHandlePromptFileRejectsAnythingOutsideTheDerivedSet(t *testing.T) {
	ch := &WebChannel{workspace: writePromptFixture(t)}
	cases := []struct {
		name string
		want int
	}{
		{"sections/acting-safely.md", http.StatusOK},
		{"GLOBAL.md", http.StatusOK},
		{"sections/../../config.yaml", http.StatusBadRequest},
		{"../config.yaml", http.StatusBadRequest},
		{"sections/nonexistent.md", http.StatusBadRequest},
		{"config.yaml", http.StatusBadRequest},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		ch.handlePromptFile(rec, httptest.NewRequest(http.MethodGet, "/api/prompts/"+c.name, nil))
		if rec.Code != c.want {
			t.Errorf("GET /api/prompts/%s = %d, want %d (body %q)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
}
