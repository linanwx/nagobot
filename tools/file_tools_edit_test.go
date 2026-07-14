package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runEdit(t *testing.T, args string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.md")
	if err := os.WriteFile(p, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Replace path placeholder with actual path.
	args = strings.ReplaceAll(args, "__PATH__", p)
	tool := &EditFileTool{workspace: dir}
	out := tool.Run(context.Background(), json.RawMessage(args))
	return p, out
}

func TestEditFile_AcceptsOldStringAlias(t *testing.T) {
	p, out := runEdit(t, `{"path":"__PATH__","old_string":"hello","new_string":"HELLO"}`)
	if strings.Contains(out, "Error") || strings.Contains(out, "error") {
		t.Fatalf("alias should succeed, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "HELLO world") {
		t.Fatalf("edit not applied; file=%q", string(b))
	}
}

func TestEditFile_RejectsUnknownField(t *testing.T) {
	_, out := runEdit(t, `{"path":"__PATH__","old_text":"hello","new_text":"HELLO","bogus":1}`)
	if !strings.Contains(out, "unknown argument") || !strings.Contains(out, "bogus") {
		t.Fatalf("expected unknown-argument error, got: %s", out)
	}
}

// An empty old_text has no unique match and would be a whole-file rewrite by
// accident. `required:"true"` catches it in parseArgs, ahead of the engine's
// own emptyOldTextError.
func TestEditFile_RejectsEmptyOldText(t *testing.T) {
	_, out := runEdit(t, `{"path":"__PATH__","old_text":"","new_text":"X"}`)
	if !IsToolError(out) || !strings.Contains(out, "old_text") {
		t.Fatalf("expected empty-old_text error, got: %s", out)
	}
}

func TestEditFile_RejectsMissingOldText(t *testing.T) {
	_, out := runEdit(t, `{"path":"__PATH__","new_text":"X"}`)
	if !IsToolError(out) || !strings.Contains(out, "old_text") {
		t.Fatalf("expected missing-old_text error, got: %s", out)
	}
}

// A dropped new_text key must fail loudly, not silently delete old_text. This
// is why editFileArgs.NewText is a *string: `required:"true"` on a plain string
// would accept a missing key as "" and quietly perform a deletion.
func TestEditFile_RejectsMissingNewText(t *testing.T) {
	p, out := runEdit(t, `{"path":"__PATH__","old_text":"hello"}`)
	if !IsToolError(out) || !strings.Contains(out, "new_text") {
		t.Fatalf("expected missing-new_text error, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "hello world\n" {
		t.Fatalf("file must be untouched; file=%q", string(b))
	}
}

// An empty new_text is the legitimate way to delete the matched text.
func TestEditFile_EmptyNewTextDeletes(t *testing.T) {
	p, out := runEdit(t, `{"path":"__PATH__","old_text":"hello ","new_text":""}`)
	if IsToolError(out) {
		t.Fatalf("empty new_text should delete, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "world\n" {
		t.Fatalf("deletion result wrong; file=%q", string(b))
	}
}

// The edits[] batch form was reverted (see editFileArgs). It must be rejected
// by name, not accepted-and-ignored, so a model trained on the old shape gets
// told to resend rather than silently making no edit.
func TestEditFile_RejectsEditsArray(t *testing.T) {
	p, out := runEdit(t, `{"path":"__PATH__","edits":[{"old_text":"hello","new_text":"HI"}]}`)
	if !IsToolError(out) || !strings.Contains(out, "edits") {
		t.Fatalf("expected edits[] to be rejected, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "hello world\n" {
		t.Fatalf("file must be untouched; file=%q", string(b))
	}
}

func TestEditFile_AliasDoesNotOverrideCanonical(t *testing.T) {
	// If both old_text and old_string are present, canonical wins.
	p, out := runEdit(t, `{"path":"__PATH__","old_text":"hello","new_text":"HI","old_string":"world","new_string":"NOPE"}`)
	if strings.Contains(out, "Error") || strings.Contains(out, "error") {
		t.Fatalf("should succeed, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "HI world") {
		t.Fatalf("canonical field should win; file=%q", string(b))
	}
}

// runEditOn writes content to a temp file then runs edit_file with args.
func runEditOn(t *testing.T, content, args string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	args = strings.ReplaceAll(args, "__PATH__", p)
	tool := &EditFileTool{workspace: dir}
	out := tool.Run(context.Background(), json.RawMessage(args))
	return p, out
}

// The pi-mono engine's fuzzy / CRLF / BOM / uniqueness behavior survives the
// revert to a single-edit interface; these pin it at the tool boundary.
// Multi-edit semantics stay covered at the engine level in edit_test.go.

func TestEditFile_PreservesCRLF(t *testing.T) {
	p, out := runEditOn(t, "a\r\nb\r\nc\r\n",
		`{"path":"__PATH__","old_text":"b","new_text":"B"}`)
	if IsToolError(out) {
		t.Fatalf("should succeed, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "a\r\nB\r\nc\r\n" {
		t.Fatalf("CRLF not preserved; file=%q", string(b))
	}
}

func TestEditFile_PreservesBOM(t *testing.T) {
	p, out := runEditOn(t, "\uFEFFhello\n",
		`{"path":"__PATH__","old_text":"hello","new_text":"world"}`)
	if IsToolError(out) {
		t.Fatalf("should succeed, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "\uFEFFworld\n" {
		t.Fatalf("BOM not preserved; file=%q", string(b))
	}
}

func TestEditFile_FuzzySmartQuotes(t *testing.T) {
	// File has smart quotes; the model's old_text uses ASCII quotes.
	p, out := runEditOn(t, "say “hi”\n",
		`{"path":"__PATH__","old_text":"say \"hi\"","new_text":"say \"bye\""}`)
	if IsToolError(out) {
		t.Fatalf("fuzzy smart-quote edit should succeed, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "say \"bye\"\n" {
		t.Fatalf("fuzzy smart-quote result wrong; file=%q", string(b))
	}
}

func TestEditFile_SingleEdit(t *testing.T) {
	p, out := runEdit(t, `{"path":"__PATH__","old_text":"hello","new_text":"HI"}`)
	if IsToolError(out) {
		t.Fatalf("single edit should work, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "HI world") {
		t.Fatalf("single edit result wrong; file=%q", string(b))
	}
}

func TestEditFile_DuplicateRejected(t *testing.T) {
	_, out := runEditOn(t, "x x x\n",
		`{"path":"__PATH__","old_text":"x","new_text":"y"}`)
	if !IsToolError(out) || !strings.Contains(out, "occurrences") {
		t.Fatalf("expected duplicate error, got: %s", out)
	}
}
