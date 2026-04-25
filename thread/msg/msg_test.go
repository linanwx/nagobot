package msg

import (
	"strings"
	"testing"
)

// ---------- BuildSystemMessage ----------

func TestBuildSystemMessage_Shape(t *testing.T) {
	out := BuildSystemMessage("error", map[string]string{"detail": "something broke"}, "this is the body")

	mapping, body, ok := ParseFrontmatter(out)
	if !ok {
		t.Fatalf("BuildSystemMessage output not parseable:\n%s", out)
	}
	if got := LookupScalar(mapping, "type"); got != "error" {
		t.Errorf("type = %q", got)
	}
	if got := LookupScalar(mapping, "sender"); got != "system" {
		t.Errorf("sender = %q", got)
	}
	if got := LookupScalar(mapping, "detail"); got != "something broke" {
		t.Errorf("detail = %q", got)
	}
	if !strings.Contains(body, "this is the body") {
		t.Errorf("body missing content: %q", body)
	}
}

func TestBuildSystemMessage_LeadingFieldsFirst(t *testing.T) {
	out := BuildSystemMessage("foo", map[string]string{"zeta": "z", "alpha": "a"}, "")
	tIdx := strings.Index(out, "type:")
	sIdx := strings.Index(out, "sender:")
	aIdx := strings.Index(out, "alpha:")
	zIdx := strings.Index(out, "zeta:")
	// Required: type and sender come before extras, extras sorted.
	if !(tIdx < sIdx && sIdx < aIdx && aIdx < zIdx) {
		t.Errorf("expected order type<sender<alpha<zeta, got:\n%s", out)
	}
}

func TestBuildSystemMessage_NoFields(t *testing.T) {
	out := BuildSystemMessage("note", nil, "body only")
	if !strings.Contains(out, "type: note") {
		t.Errorf("type missing: %s", out)
	}
	if !strings.Contains(out, "sender: system") {
		t.Errorf("sender missing: %s", out)
	}
	if !strings.Contains(out, "body only") {
		t.Errorf("body missing: %s", out)
	}
}

func TestBuildSystemMessage_EmptyBody(t *testing.T) {
	out := BuildSystemMessage("ping", map[string]string{"k": "v"}, "")
	mapping, body, ok := ParseFrontmatter(out)
	if !ok {
		t.Fatalf("not parseable: %s", out)
	}
	if LookupScalar(mapping, "type") != "ping" {
		t.Errorf("type wrong")
	}
	if strings.TrimSpace(body) != "" {
		t.Errorf("body should be empty after trim, got %q", body)
	}
}

func TestBuildSystemMessage_MultiLineBody(t *testing.T) {
	body := "line one\nline two\nline three"
	out := BuildSystemMessage("ping", nil, body)
	_, gotBody, ok := ParseFrontmatter(out)
	if !ok {
		t.Fatalf("not parseable: %s", out)
	}
	if !strings.Contains(gotBody, body) {
		t.Errorf("multi-line body lost: got %q, want contains %q", gotBody, body)
	}
}

func TestBuildSystemMessage_BodyWithFrontmatterLikeContent(t *testing.T) {
	// Body that itself contains `---` lines must NOT be treated as nested
	// frontmatter to merge — body is opaque.
	body := "embedded:\n---\ninner: yaml\n---\ndoc"
	out := BuildSystemMessage("note", map[string]string{"k": "v"}, body)
	mapping, gotBody, ok := ParseFrontmatter(out)
	if !ok {
		t.Fatalf("not parseable: %s", out)
	}
	if LookupScalar(mapping, "k") != "v" {
		t.Errorf("k value lost")
	}
	if !strings.Contains(gotBody, body) {
		t.Errorf("nested-like body lost:\nwant contains: %q\ngot: %q", body, gotBody)
	}
}

func TestBuildSystemMessage_QuoteSafety(t *testing.T) {
	// Field values with colons, quotes, backslashes, special YAML chars must
	// round-trip safely — old manual quoting heuristics could corrupt these.
	tricky := map[string]string{
		"path":   "/etc/hosts:80",
		"json":   `{"k":"v"}`,
		"quote":  `"already quoted"`,
		"colon":  "key: value with colon",
		"yes":    "yes",
		"newln":  "line1\nline2",
	}
	out := BuildSystemMessage("test", tricky, "")
	mapping, _, ok := ParseFrontmatter(out)
	if !ok {
		t.Fatalf("not parseable: %s", out)
	}
	for k, want := range tricky {
		if got := LookupScalar(mapping, k); got != want {
			t.Errorf("%s round-trip: got %q, want %q", k, got, want)
		}
	}
}

func TestBuildSystemMessage_Idempotent(t *testing.T) {
	first := BuildSystemMessage("foo", map[string]string{"a": "1", "b": "2"}, "body")
	for i := 0; i < 20; i++ {
		got := BuildSystemMessage("foo", map[string]string{"a": "1", "b": "2"}, "body")
		if got != first {
			t.Errorf("non-deterministic on iter %d", i)
		}
	}
}
