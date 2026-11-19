package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// declareForTest registers a prompt under a key unique to the test and
// removes it afterwards, so tests do not leak declarations into Check.
func declareForTest(t *testing.T, key string, opts ...Option) *Prompt {
	t.Helper()
	p := Declare(key, opts...)
	t.Cleanup(func() {
		declMu.Lock()
		delete(declared, key)
		declMu.Unlock()
	})
	return p
}

func captureErrors(t *testing.T) *[]string {
	t.Helper()
	var got []string
	prev := reportError
	reportError = func(msg string, kv ...any) { got = append(got, msg) }
	t.Cleanup(func() { reportError = prev })
	return &got
}

func TestParseChecksPlaceholdersAgainstDeclaration(t *testing.T) {
	p := &Prompt{key: "x", vars: []string{"A"}}
	if _, err := parse(p, "uses {{A}}"); err != nil {
		t.Fatalf("matching placeholders rejected: %v", err)
	}
	if _, err := parse(p, "forgot it"); err == nil || !strings.Contains(err.Error(), "missing [A]") {
		t.Fatalf("missing placeholder not reported: %v", err)
	}
	if _, err := parse(p, "{{A}} and {{B}}"); err == nil || !strings.Contains(err.Error(), "unknown [B]") {
		t.Fatalf("unknown placeholder not reported: %v", err)
	}
	if _, err := parse(p, "---\nnote: n\n---\n\n"); err == nil {
		t.Fatal("empty body accepted")
	}
}

func TestOneLineJoinsTheFileIntoOneLine(t *testing.T) {
	p := &Prompt{key: "x", oneLine: true}
	f, err := parse(p, "---\nnote: \"for humans\"\n---\nFirst sentence.\nSecond one;\n\n(a) third.\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := "First sentence. Second one; (a) third."; f.body != want {
		t.Fatalf("got %q, want %q", f.body, want)
	}
}

func TestMultiLinePromptKeepsItsLineBreaks(t *testing.T) {
	f, err := parse(&Prompt{key: "x"}, "a\n\n  - b\n")
	if err != nil {
		t.Fatal(err)
	}
	if f.body != "a\n\n  - b" {
		t.Fatalf("got %q", f.body)
	}
}

// A value is inserted literally: a pinned message that happens to contain
// "{{PINS_DIR}}" must not be expanded a second time.
func TestFillIsSinglePass(t *testing.T) {
	p := &Prompt{key: "x", vars: []string{"A", "B"}}
	got := fill(p, "{{A}}|{{B}}", Vars{"A": "{{B}}", "B": "b"})
	if got != "{{B}}|b" {
		t.Fatalf("got %q", got)
	}
}

func TestFillReportsMissingAndUndeclaredVars(t *testing.T) {
	errs := captureErrors(t)
	p := &Prompt{key: "x", vars: []string{"A"}}
	if got := fill(p, "[{{A}}]", Vars{"Z": "z"}); got != "[{{A}}]" {
		t.Fatalf("a missing var must stay visible, got %q", got)
	}
	if len(*errs) != 2 {
		t.Fatalf("want 2 errors (missing A, undeclared Z), got %v", *errs)
	}
}

func TestToolParamsOnlyInToolFiles(t *testing.T) {
	if _, err := parse(&Prompt{key: "x"}, "---\nparams:\n  a: b\n---\nbody"); err == nil {
		t.Fatal("params accepted outside tools/")
	}
	f, err := parse(&Prompt{key: "tools/x", tool: true}, "---\nparams:\n  a: \"desc\"\n  a.b: \"nested\"\n---\nbody")
	if err != nil {
		t.Fatal(err)
	}
	if f.params["a.b"] != "nested" {
		t.Fatalf("params = %v", f.params)
	}
}

func TestDescribeFillsSchemaByDottedPath(t *testing.T) {
	p := declareForTest(t, "tools/zz-describe-test", WithVars("V"), func(p *Prompt) { p.tool = true })
	dir := t.TempDir()
	writeFile(t, dir, "tools/zz-describe-test.md", "---\nparams:\n  list: \"the list {{V}}\"\n  list.name: \"a name\"\n---\ndesc {{V}}")
	useDirOnly(t, dir)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"list": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":       "object",
					"properties": map[string]any{"name": map[string]any{"type": "string"}},
				},
			},
		},
	}
	desc := p.Describe(schema, Vars{"V": "v"})
	if desc != "desc v" {
		t.Fatalf("desc = %q", desc)
	}
	list := schema["properties"].(map[string]any)["list"].(map[string]any)
	if list["description"] != "the list v" {
		t.Fatalf("list description = %v", list["description"])
	}
	name := list["items"].(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)
	if name["description"] != "a name" {
		t.Fatalf("nested description = %v", name["description"])
	}
}

func TestCheckFindsMissingAndUndeclaredFiles(t *testing.T) {
	declareForTest(t, "zz/declared-only")
	err := Check(fstest.MapFS{"zz/orphan.md": {Data: []byte("x")}})
	if err == nil {
		t.Fatal("Check passed")
	}
	for _, want := range []string{"zz/declared-only.md: missing", "zz/orphan.md: no code declares this prompt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

// A bad live edit keeps the last good text instead of serving nothing, and a
// good edit is picked up without a restart.
func TestReloadKeepsPreviousTextOnABadEdit(t *testing.T) {
	p := declareForTest(t, "zz/reload", WithVars("X"))
	dir := t.TempDir()
	writeFile(t, dir, "zz/reload.md", "v1 {{X}}")
	useDirOnly(t, dir)
	errs := captureErrors(t)

	if got := p.Render(Vars{"X": "x"}); got != "v1 x" {
		t.Fatalf("got %q", got)
	}
	writeFile(t, dir, "zz/reload.md", "v2 dropped the placeholder")
	forceReloadCheck()
	if got := p.Render(Vars{"X": "x"}); got != "v1 x" {
		t.Fatalf("bad edit replaced the text: %q", got)
	}
	if len(*errs) == 0 {
		t.Fatal("bad edit was not reported")
	}
	writeFile(t, dir, "zz/reload.md", "v3 {{X}}")
	forceReloadCheck()
	if got := p.Render(Vars{"X": "x"}); got != "v3 x" {
		t.Fatalf("good edit not picked up: %q", got)
	}
}

func TestUseDirRefusesAnIncompleteSet(t *testing.T) {
	declareForTest(t, "zz/required")
	if err := UseDir(t.TempDir()); err == nil || !strings.Contains(err.Error(), "zz/required.md: missing") {
		t.Fatalf("UseDir accepted a directory without a declared prompt: %v", err)
	}
}

func TestNote(t *testing.T) {
	if got := Note("---\nnote: \"what it is\"\n---\nbody"); got != "what it is" {
		t.Fatalf("got %q", got)
	}
	if got := Note("no frontmatter"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Distinct mtimes so the snapshot sees every edit.
	future := time.Now().Add(time.Duration(len(content)) * time.Second)
	_ = os.Chtimes(path, future, future)
}

// useDirOnly points the store at dir without UseDir's completeness check,
// which the test's own declarations alone could never pass alongside every
// other package's. Restores the previous store afterwards.
func useDirOnly(t *testing.T, dir string) {
	t.Helper()
	raw, err := readFS(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	prevErr := reportError
	reportError = func(string, ...any) {} // other declarations are absent here by design
	s := &store{dir: dir, files: parseAll(raw, nil, dir), snapshot: dirSnapshot(dir), lastCheck: time.Now()}
	reportError = prevErr
	activeMu.Lock()
	prev := activeStore
	activeStore = s
	activeMu.Unlock()
	t.Cleanup(func() {
		activeMu.Lock()
		activeStore = prev
		activeMu.Unlock()
	})
}

func forceReloadCheck() {
	s := active()
	s.mu.Lock()
	s.lastCheck = time.Time{}
	s.mu.Unlock()
}
