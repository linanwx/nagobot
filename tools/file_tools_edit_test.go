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
	if !strings.Contains(out, "missing or empty required argument") || !strings.Contains(out, "old_text") {
		t.Fatalf("expected empty-old_text error, got: %s", out)
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
