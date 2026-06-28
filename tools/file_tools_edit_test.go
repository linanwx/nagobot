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

func TestEditFile_RejectsEmptyOldText(t *testing.T) {
	_, out := runEdit(t, `{"path":"__PATH__","old_text":"","new_text":"X"}`)
	if !IsToolError(out) || !strings.Contains(out, "must not be empty") {
		t.Fatalf("expected empty-old_text error, got: %s", out)
	}
}

func TestEditFile_RejectsMissingEdits(t *testing.T) {
	_, out := runEdit(t, `{"path":"__PATH__"}`)
	if !IsToolError(out) || !strings.Contains(out, "edits") {
		t.Fatalf("expected missing-edits error, got: %s", out)
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

func TestEditFile_MultipleEdits(t *testing.T) {
	p, out := runEditOn(t, "a=1\nb=2\nc=3\n",
		`{"path":"__PATH__","edits":[{"old_text":"a=1","new_text":"a=10"},{"old_text":"c=3","new_text":"c=30"}]}`)
	if IsToolError(out) {
		t.Fatalf("multi-edit should succeed, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "a=10\nb=2\nc=30\n" {
		t.Fatalf("multi-edit result wrong; file=%q", string(b))
	}
}

func TestEditFile_PreservesCRLF(t *testing.T) {
	p, out := runEditOn(t, "a\r\nb\r\nc\r\n",
		`{"path":"__PATH__","edits":[{"old_text":"b","new_text":"B"}]}`)
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
		`{"path":"__PATH__","edits":[{"old_text":"hello","new_text":"world"}]}`)
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
		`{"path":"__PATH__","edits":[{"old_text":"say \"hi\"","new_text":"say \"bye\""}]}`)
	if IsToolError(out) {
		t.Fatalf("fuzzy smart-quote edit should succeed, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "say \"bye\"\n" {
		t.Fatalf("fuzzy smart-quote result wrong; file=%q", string(b))
	}
}

func TestEditFile_EditsAsJSONString(t *testing.T) {
	// Some models send edits as a JSON-encoded string.
	p, out := runEdit(t, `{"path":"__PATH__","edits":"[{\"old_text\":\"hello\",\"new_text\":\"HI\"}]"}`)
	if IsToolError(out) {
		t.Fatalf("edits-as-string should parse, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "HI world") {
		t.Fatalf("edits-as-string result wrong; file=%q", string(b))
	}
}

func TestEditFile_LegacySingleForm(t *testing.T) {
	p, out := runEdit(t, `{"path":"__PATH__","old_text":"hello","new_text":"HI"}`)
	if IsToolError(out) {
		t.Fatalf("legacy single form should work, got: %s", out)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "HI world") {
		t.Fatalf("legacy single form result wrong; file=%q", string(b))
	}
}

func TestEditFile_OverlapRejected(t *testing.T) {
	_, out := runEditOn(t, "abcdef\n",
		`{"path":"__PATH__","edits":[{"old_text":"abc","new_text":"X"},{"old_text":"bcd","new_text":"Y"}]}`)
	if !IsToolError(out) || !strings.Contains(out, "overlap") {
		t.Fatalf("expected overlap error, got: %s", out)
	}
}

func TestEditFile_DuplicateRejected(t *testing.T) {
	_, out := runEditOn(t, "x x x\n",
		`{"path":"__PATH__","edits":[{"old_text":"x","new_text":"y"}]}`)
	if !IsToolError(out) || !strings.Contains(out, "occurrences") {
		t.Fatalf("expected duplicate error, got: %s", out)
	}
}
