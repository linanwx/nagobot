package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runRead(t *testing.T, content, args string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	args = strings.ReplaceAll(args, "__PATH__", p)
	tool := &ReadFileTool{workspace: dir}
	out := tool.Run(context.Background(), json.RawMessage(args))
	return p, out
}

func TestReadFile_Normal(t *testing.T) {
	_, out := runRead(t, "a\nb\nc\n", `{"path":"__PATH__"}`)
	if IsToolError(out) {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.Contains(out, "truncated") {
		t.Fatalf("should not be truncated: %s", out)
	}
	if !strings.Contains(out, "1\ta") {
		t.Fatalf("expected line-numbered content, got: %s", out)
	}
}

func TestReadFile_SingleLongLineErrors(t *testing.T) {
	long := strings.Repeat("x", 60*1024) // one 60KB line, over the 50KB limit
	_, out := runRead(t, long+"\n", `{"path":"__PATH__"}`)
	if !IsToolError(out) || !strings.Contains(out, "exceeds the 50KB read limit") {
		t.Fatalf("expected long-line error, got: %s", out)
	}
	if strings.Contains(out, strings.Repeat("x", 100)) {
		t.Fatalf("must not dump content on long-line error: %s", out[:200])
	}
}

func TestReadFile_ByteCapTruncates(t *testing.T) {
	// 100 lines x 1KB = ~100KB total, but no single line over the limit.
	line := strings.Repeat("y", 1000)
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString(line)
		b.WriteString("\n")
	}
	_, out := runRead(t, b.String(), `{"path":"__PATH__"}`)
	if IsToolError(out) {
		t.Fatalf("byte cap should truncate, not error: %s", out)
	}
	if !strings.Contains(out, "truncated") || !strings.Contains(out, "next_offset") {
		head := out
		if len(head) > 200 {
			head = head[:200]
		}
		t.Fatalf("expected truncation + next_offset, got header: %s", head)
	}
}

func TestReadFile_ByteCapContinuationReadsRest(t *testing.T) {
	line := strings.Repeat("z", 1000)
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString(line)
		b.WriteString("\n")
	}
	// Continue from a deep offset; the remaining tail fits under the byte cap.
	_, out := runRead(t, b.String(), `{"path":"__PATH__","offset":91}`)
	if IsToolError(out) {
		t.Fatalf("continuation read should succeed: %s", out)
	}
	if strings.Contains(out, "truncated") {
		t.Fatalf("tail under byte cap should not be truncated: %s", out)
	}
}
