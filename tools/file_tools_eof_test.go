package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stripReadLineNumbers undoes read_file's "%d\t" line prefixes, which is the
// one transformation a model must perform by hand to turn a read into an
// old_text. Tests that go through it are asserting the property that matters:
// what the model can SEE is something it can quote back.
var readLinePrefix = regexp.MustCompile(`(?m)^\d+\t`)

func readBody(t *testing.T, out string) string {
	t.Helper()
	if IsToolError(out) {
		t.Fatalf("read failed: %s", out)
	}
	_, body, found := strings.Cut(out, "\n---\n")
	if !found {
		t.Fatalf("no frontmatter separator in read output: %s", out)
	}
	return readLinePrefix.ReplaceAllString(strings.TrimPrefix(body, "\n"), "")
}

// A file ending in "\n" used to render a phantom final line, telling the model
// the file had one more newline at EOF than it does.
func TestReadFile_NoPhantomTrailingLine(t *testing.T) {
	_, out := runRead(t, "a\nb\n", `{"path":"__PATH__"}`)
	if strings.Contains(out, "3\t") {
		t.Fatalf("rendered a phantom third line: %q", out)
	}
	if !strings.Contains(out, "total: 2") {
		t.Fatalf("expected total: 2, got: %q", out)
	}
	if got := readBody(t, out); got != "a\nb\n" {
		t.Fatalf("body = %q, want %q", got, "a\nb\n")
	}
}

// Once every rendered line is terminated with "\n", the file's own EOF
// convention is invisible in the body — so it is stated in the frontmatter.
func TestReadFile_ReportsEndsWithNewline(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    string
	}{
		{"a\nb\n", "ends_with_newline: true"},
		{"a\nb", "ends_with_newline: false"},
	} {
		_, out := runRead(t, tc.content, `{"path":"__PATH__"}`)
		if !strings.Contains(out, tc.want) {
			t.Fatalf("content %q: expected %q in: %s", tc.content, tc.want, out)
		}
	}
}

// The claim is about the END of the file; a window that stops short must not
// make it, because it is describing bytes the model cannot see.
func TestReadFile_EndsWithNewlineOnlyWhenWindowReachesEOF(t *testing.T) {
	_, out := runRead(t, "a\nb\nc\n", `{"path":"__PATH__","limit":1}`)
	if strings.Contains(out, "ends_with_newline") {
		t.Fatalf("partial window must not claim an EOF fact: %s", out)
	}
}

// The phantom line was also what `tail` counted back from, so on any file
// ending with a newline — i.e. almost all of them — tail=1 returned the empty
// line after the content instead of the last line of it.
func TestReadFile_TailReturnsTheLastRealLine(t *testing.T) {
	_, out := runRead(t, "a\nb\nlast\n", `{"path":"__PATH__","tail":1}`)
	if got := readBody(t, out); got != "last\n" {
		t.Fatalf("tail=1 returned %q, want %q", got, "last\n")
	}
}

// The end-to-end property, and the one that reproduces the measured failure:
// take read_file's own output, strip the line numbers, and use its tail as
// old_text. Appending is the only thing a single replacement can do at EOF, so
// this is the shape every nightly USER.md update takes.
func TestReadThenAppendAtEOFRoundTrips(t *testing.T) {
	for _, content := range []string{"first\nlast line\n", "first\nlast line"} {
		dir := t.TempDir()
		p := filepath.Join(dir, "USER.md")
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		reader := &ReadFileTool{workspace: dir}
		body := readBody(t, reader.Run(context.Background(), json.RawMessage(
			fmt.Sprintf(`{"path":%q}`, p))))

		// The model quotes the tail of what it was shown, verbatim.
		_, tail, _ := strings.Cut(body, "\n")
		args, err := json.Marshal(map[string]string{
			"path": p, "old_text": tail, "new_text": tail + "appended\n",
		})
		if err != nil {
			t.Fatal(err)
		}

		editor := &EditFileTool{workspace: dir}
		if out := editor.Run(context.Background(), json.RawMessage(args)); IsToolError(out) {
			t.Fatalf("content %q: quoting read_file's own tail must match: %s", content, out)
		}
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(got), "first\nlast line\nappended") {
			t.Fatalf("content %q: append landed wrong: %q", content, string(got))
		}
	}
}

// A small file is handed back whole, because the model's next guess is only as
// good as the bytes it has.
func TestEditMismatchInlinesSmallFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.md")
	if err := os.WriteFile(p, []byte("alpha\nbeta\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &EditFileTool{workspace: dir}
	out := tool.Run(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"path":%q,"old_text":"gamma","new_text":"delta"}`, p)))
	if !IsToolError(out) {
		t.Fatalf("expected a mismatch error, got: %s", out)
	}
	if !strings.Contains(out, "alpha\nbeta\n") {
		t.Fatalf("expected the file's contents in the error, got: %s", out)
	}
}

// Past the line cap the error would cost more context than the retry is worth,
// so the model is pointed at the two calls that recover the bytes instead.
func TestEditMismatchPointsLargeFileAtGrep(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.md")
	body := strings.Repeat("filler line\n", editHintMaxLines+1)
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &EditFileTool{workspace: dir}
	out := tool.Run(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"path":%q,"old_text":"gamma","new_text":"delta"}`, p)))
	if !IsToolError(out) {
		t.Fatalf("expected a mismatch error, got: %s", out)
	}
	if strings.Contains(out, body) {
		t.Fatalf("must not inline a file over the cap: %s", out)
	}
	for _, want := range []string{"grep(", "read_file(", "too large to quote"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in the recovery hint, got: %s", want, out)
		}
	}
	// The recipe's path arguments must be pasteable. The display form carries a
	// "(resolved: …)" annotation, and a model copying that into the next call
	// would be passing a path that does not exist.
	recipe := out[strings.Index(out, "grep("):]
	if strings.Contains(recipe, "resolved:") {
		t.Fatalf("recipe must quote the resolved path, not the display form: %s", recipe)
	}
	if !strings.Contains(recipe, fmt.Sprintf("%q", p)) {
		t.Fatalf("recipe must quote %q, got: %s", p, recipe)
	}
}

// Only the not-found failure gets a hint. A duplicate already tells the model
// exactly what to do, and quoting the file at it would just be noise.
func TestEditDuplicateGetsNoFileDump(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.md")
	if err := os.WriteFile(p, []byte("dup\ndup\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &EditFileTool{workspace: dir}
	out := tool.Run(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"path":%q,"old_text":"dup","new_text":"x"}`, p)))
	if !strings.Contains(out, "occurrences") {
		t.Fatalf("expected a duplicate error, got: %s", out)
	}
	if strings.Contains(out, "copy old_text verbatim") {
		t.Fatalf("duplicate must not carry the not-found hint: %s", out)
	}
}
