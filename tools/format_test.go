package tools

import (
	"strings"
	"testing"

	"github.com/linanwx/nagobot/thread/msg"
)

// ---------- toolResult ----------

func TestToolResult_BasicShape(t *testing.T) {
	out := toolResult("read_file", map[string]any{"path": "/tmp/x", "size": 1234}, "file body content")

	mapping, body, ok := msg.ParseFrontmatter(out)
	if !ok {
		t.Fatalf("output not parseable: %q", out)
	}
	if got := msg.LookupScalar(mapping, "tool"); got != "read_file" {
		t.Errorf("tool = %q", got)
	}
	if got := msg.LookupScalar(mapping, "status"); got != "ok" {
		t.Errorf("status = %q", got)
	}
	if got := msg.LookupScalar(mapping, "path"); got != "/tmp/x" {
		t.Errorf("path = %q", got)
	}
	if got := msg.LookupScalar(mapping, "size"); got != "1234" {
		t.Errorf("size = %q", got)
	}
	if !strings.Contains(body, "file body content") {
		t.Errorf("body missing content, got %q", body)
	}
}

func TestToolResult_KeyOrder(t *testing.T) {
	out := toolResult("foo", map[string]any{"zzz": 1, "aaa": 2, "mid": 3}, "")
	tIdx := strings.Index(out, "tool:")
	sIdx := strings.Index(out, "status:")
	aIdx := strings.Index(out, "aaa:")
	mIdx := strings.Index(out, "mid:")
	zIdx := strings.Index(out, "zzz:")
	if !(tIdx < sIdx && sIdx < aIdx && aIdx < mIdx && mIdx < zIdx) {
		t.Errorf("expected order tool<status<aaa<mid<zzz, got:\n%s", out)
	}
}

func TestToolResult_QuotesValueWithColon(t *testing.T) {
	// Values that need quoting must be quoted by yaml.Marshal — old manual
	// formatYAMLField had heuristics that could miss edge cases.
	out := toolResult("foo", map[string]any{"path": "/etc/hosts:80"}, "")
	mapping, _, ok := msg.ParseFrontmatter(out)
	if !ok {
		t.Fatalf("output not parseable: %q", out)
	}
	if got := msg.LookupScalar(mapping, "path"); got != "/etc/hosts:80" {
		t.Errorf("path round-trip mismatch: got %q", got)
	}
}

func TestToolResult_NativeTypes(t *testing.T) {
	out := toolResult("foo", map[string]any{"flag": true, "n": 42}, "")
	if !strings.Contains(out, "flag: true\n") {
		t.Errorf("bool not emitted as bare: %s", out)
	}
	if !strings.Contains(out, "n: 42\n") {
		t.Errorf("int not emitted as bare: %s", out)
	}
}

func TestToolResult_EmptyBody(t *testing.T) {
	out := toolResult("foo", map[string]any{"x": "1"}, "")
	if strings.Contains(out, "Error:") {
		t.Errorf("unexpected Error in empty-body result: %s", out)
	}
	mapping, body, ok := msg.ParseFrontmatter(out)
	if !ok {
		t.Fatalf("output not parseable: %s", out)
	}
	if msg.LookupScalar(mapping, "x") != "1" {
		t.Errorf("x not preserved")
	}
	if strings.TrimSpace(body) != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestToolResult_Determinism(t *testing.T) {
	// Map iteration order is randomized; output must be stable.
	fields := map[string]any{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5}
	first := toolResult("foo", fields, "body")
	for i := 0; i < 30; i++ {
		if got := toolResult("foo", fields, "body"); got != first {
			t.Fatalf("non-deterministic output on iter %d:\nfirst:%s\nthis: %s", i, first, got)
		}
	}
}

// ---------- toolError ----------

func TestToolError_StartsWithErrorLegacyMarker(t *testing.T) {
	out := toolError("foo", "something broke")
	if !strings.Contains(out, "Error: something broke") {
		t.Errorf("output missing legacy `Error:` body: %s", out)
	}
	mapping, _, ok := msg.ParseFrontmatter(out)
	if !ok {
		t.Fatalf("not parseable: %s", out)
	}
	if msg.LookupScalar(mapping, "status") != "error" {
		t.Errorf("status should be error")
	}
}

// ---------- IsToolError ----------

func TestIsToolError(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"yaml-error", toolError("foo", "boom"), true},
		{"yaml-ok", toolResult("foo", nil, ""), false},
		{"legacy-error-prefix", "Error: legacy", true},
		{"plain-text", "this is just text", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsToolError(c.in); got != c.want {
				t.Errorf("IsToolError(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// ---------- CmdResult / CmdError / CmdOutput ----------

func TestCmdResult_Shape(t *testing.T) {
	out := CmdResult("set-summary", map[string]any{"session": "discord:123"}, "Saved.")
	mapping, body, ok := msg.ParseFrontmatter(out)
	if !ok {
		t.Fatalf("not parseable: %s", out)
	}
	if msg.LookupScalar(mapping, "command") != "set-summary" {
		t.Errorf("command mismatch")
	}
	if msg.LookupScalar(mapping, "status") != "ok" {
		t.Errorf("status mismatch")
	}
	if msg.LookupScalar(mapping, "session") != "discord:123" {
		t.Errorf("session value not properly quoted: got %q", msg.LookupScalar(mapping, "session"))
	}
	if !strings.Contains(body, "Saved.") {
		t.Errorf("body missing")
	}
}

func TestCmdError_Shape(t *testing.T) {
	out := CmdError("foo", "bad input")
	if !strings.Contains(out, "Error: bad input") {
		t.Errorf("legacy Error: marker missing: %s", out)
	}
	mapping, _, ok := msg.ParseFrontmatter(out)
	if !ok {
		t.Fatalf("not parseable: %s", out)
	}
	if msg.LookupScalar(mapping, "status") != "error" {
		t.Errorf("status should be error")
	}
}

func TestCmdOutput_OrderPreserved(t *testing.T) {
	// CmdOutput is the explicit-order helper for CLI sites that need a
	// fixed (non-alphabetical) field sequence.
	out := CmdOutput([][2]string{
		{"command", "upload-html"},
		{"status", "ok"},
		{"url", "https://x"},
		{"site_id", "abc"},
		{"file", "/tmp/a"},
		{"size_bytes", "1024"},
	}, "")

	want := []string{"command:", "status:", "url:", "site_id:", "file:", "size_bytes:"}
	pos := -1
	for _, k := range want {
		idx := strings.Index(out, k)
		if idx < 0 {
			t.Errorf("key %q missing from output:\n%s", k, out)
			return
		}
		if idx <= pos {
			t.Errorf("key %q out of order in:\n%s", k, out)
		}
		pos = idx
	}
}

func TestCmdOutput_RoundTripValues(t *testing.T) {
	out := CmdOutput([][2]string{
		{"command", "x"},
		{"weird_path", "/etc/hosts:80"},
		{"yes", "yes"},
		{"truthy", "true"},
	}, "")
	mapping, _, ok := msg.ParseFrontmatter(out)
	if !ok {
		t.Fatalf("not parseable: %s", out)
	}
	if got := msg.LookupScalar(mapping, "weird_path"); got != "/etc/hosts:80" {
		t.Errorf("weird_path lost via quoting: %q", got)
	}
	// "yes" string round-trips as bool true through Lookup (yaml.v3 emits
	// `yes: yes` and parses both yeses as bool, value becomes "true").
	// Verify the round-trip is consistent — what matters is that the output
	// IS valid YAML and parses without error.
	if msg.LookupScalar(mapping, "yes") == "" {
		t.Errorf("yes key missing")
	}
	if msg.LookupScalar(mapping, "truthy") == "" {
		t.Errorf("truthy key missing")
	}
}
