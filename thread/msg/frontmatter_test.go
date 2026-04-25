package msg

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ---------- SplitFrontmatter ----------

func TestSplitFrontmatter_Basic(t *testing.T) {
	yamlBlock, body, ok := SplitFrontmatter("---\nfoo: bar\nbaz: qux\n---\nhello body")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if yamlBlock != "foo: bar\nbaz: qux" {
		t.Errorf("yamlBlock = %q", yamlBlock)
	}
	if body != "hello body" {
		t.Errorf("body = %q", body)
	}
}

func TestSplitFrontmatter_NoFrontmatter(t *testing.T) {
	_, body, ok := SplitFrontmatter("just a regular string")
	if ok {
		t.Error("expected ok=false for input without frontmatter")
	}
	if body != "just a regular string" {
		t.Errorf("body should fall through unchanged, got %q", body)
	}
}

func TestSplitFrontmatter_UnclosedFence(t *testing.T) {
	_, _, ok := SplitFrontmatter("---\nfoo: bar\nno closing fence")
	if ok {
		t.Error("expected ok=false when no closing fence")
	}
}

func TestSplitFrontmatter_ClosingFenceAtEOF(t *testing.T) {
	// `---` at end without trailing newline (legacy format from marshalCompressed).
	yamlBlock, body, ok := SplitFrontmatter("---\nfoo: bar\n---")
	if !ok {
		t.Fatal("expected ok=true for closing fence at EOF")
	}
	if yamlBlock != "foo: bar" {
		t.Errorf("yamlBlock = %q", yamlBlock)
	}
	if body != "" {
		t.Errorf("body should be empty, got %q", body)
	}
}

func TestSplitFrontmatter_NestedFrontmatterInBody(t *testing.T) {
	// The body itself contains another `---\n...---\n` block. Only the OUTER
	// pair should be consumed by Split; the inner is preserved verbatim.
	content := "---\nouter: 1\n---\n---\ninner: 2\n---\ninner body"
	yamlBlock, body, ok := SplitFrontmatter(content)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if yamlBlock != "outer: 1" {
		t.Errorf("outer yamlBlock = %q, want %q", yamlBlock, "outer: 1")
	}
	if body != "---\ninner: 2\n---\ninner body" {
		t.Errorf("body should preserve inner frontmatter verbatim, got %q", body)
	}
}

func TestSplitFrontmatter_MultiLineBlockScalarNotFenceMistaken(t *testing.T) {
	// A block scalar value contains "---" inside (indented), and there are
	// also `---` patterns inside body text. The closing fence must still be
	// the first BARE `---` line, not anything inside the block scalar.
	content := "---\n" +
		"action: |-\n" +
		"    line1\n" +
		"    --- this is part of the action, not a fence\n" +
		"    line3\n" +
		"sender: system\n" +
		"---\n" +
		"the body"
	yamlBlock, body, ok := SplitFrontmatter(content)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !strings.Contains(yamlBlock, "--- this is part of the action") {
		t.Errorf("indented `---` inside block scalar should remain in yamlBlock, got: %s", yamlBlock)
	}
	if !strings.HasSuffix(yamlBlock, "sender: system") {
		t.Errorf("yamlBlock should end with `sender: system`, got: %s", yamlBlock)
	}
	if body != "the body" {
		t.Errorf("body = %q, want %q", body, "the body")
	}
}

// ---------- ParseFrontmatter ----------

func TestParseFrontmatter_Basic(t *testing.T) {
	mapping, body, ok := ParseFrontmatter("---\nfoo: bar\nn: 42\n---\nbody text")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got := LookupScalar(mapping, "foo"); got != "bar" {
		t.Errorf("foo = %q", got)
	}
	if got := LookupScalar(mapping, "n"); got != "42" {
		t.Errorf("n = %q", got)
	}
	if body != "body text" {
		t.Errorf("body = %q", body)
	}
}

func TestParseFrontmatter_MultiLineBlockScalar(t *testing.T) {
	content := "---\n" +
		"action: |-\n" +
		"    line one\n" +
		"    line two\n" +
		"    MUST NOT: do this\n" +
		"sender: system\n" +
		"---\n" +
		"body"
	mapping, _, ok := ParseFrontmatter(content)
	if !ok {
		t.Fatal("expected ok=true")
	}
	action := LookupScalar(mapping, "action")
	if !strings.Contains(action, "line one") {
		t.Errorf("action did not retain block content, got %q", action)
	}
	if !strings.Contains(action, "MUST NOT: do this") {
		t.Errorf("action did not retain colon-containing line, got %q", action)
	}
	if got := LookupScalar(mapping, "sender"); got != "system" {
		t.Errorf("sender = %q", got)
	}
	// `MUST NOT` must NOT be parsed as a top-level key (was the original bug).
	if got := LookupScalar(mapping, "MUST NOT"); got != "" {
		t.Errorf("MUST NOT should not be a top-level key, got %q", got)
	}
}

func TestParseFrontmatter_MalformedYAML(t *testing.T) {
	// Use unbalanced flow brackets to force an actual YAML parse error.
	_, _, ok := ParseFrontmatter("---\nfoo: [oops\n---\n")
	if ok {
		t.Error("expected ok=false for malformed YAML")
	}
}

func TestParseFrontmatter_NotAMapping(t *testing.T) {
	// Top-level YAML is a sequence, not a mapping.
	_, _, ok := ParseFrontmatter("---\n- a\n- b\n---\n")
	if ok {
		t.Error("expected ok=false when YAML root is not a mapping")
	}
}

func TestParseFrontmatter_NestedFrontmatterPreserved(t *testing.T) {
	content := "---\nouter: 1\n---\n---\ninner: 2\n---\nbody"
	mapping, body, ok := ParseFrontmatter(content)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got := LookupScalar(mapping, "outer"); got != "1" {
		t.Errorf("outer = %q", got)
	}
	if !strings.HasPrefix(body, "---\ninner: 2\n---\n") {
		t.Errorf("inner frontmatter must be preserved verbatim in body, got %q", body)
	}
}

// ---------- BuildFrontmatter ----------

func TestBuildFrontmatter_RoundTrip(t *testing.T) {
	mapping := NewMapping()
	AppendScalarPair(mapping, "source", "telegram")
	AppendScalarPair(mapping, "sender", "user")

	out := BuildFrontmatter(mapping, "\nhello world")

	// Should round-trip to same mapping + same body.
	parsed, body, ok := ParseFrontmatter(out)
	if !ok {
		t.Fatalf("round-trip parse failed: %s", out)
	}
	if got := LookupScalar(parsed, "source"); got != "telegram" {
		t.Errorf("source = %q", got)
	}
	if got := LookupScalar(parsed, "sender"); got != "user" {
		t.Errorf("sender = %q", got)
	}
	if body != "\nhello world" {
		t.Errorf("body = %q", body)
	}
}

func TestBuildFrontmatter_PreservesKeyOrder(t *testing.T) {
	// Order matters for prefix-cache stability: the bytes hashed by the LLM
	// API must not flip-flop between runs.
	mapping := NewMapping()
	AppendScalarPair(mapping, "z", "1")
	AppendScalarPair(mapping, "a", "2")
	AppendScalarPair(mapping, "m", "3")
	out := BuildFrontmatter(mapping, "")
	zIdx := strings.Index(out, "z:")
	aIdx := strings.Index(out, "a:")
	mIdx := strings.Index(out, "m:")
	if !(zIdx < aIdx && aIdx < mIdx) {
		t.Errorf("expected key order z<a<m, got positions z=%d a=%d m=%d in:\n%s", zIdx, aIdx, mIdx, out)
	}
}

func TestBuildFrontmatter_EmptyBody(t *testing.T) {
	mapping := NewMapping()
	AppendScalarPair(mapping, "k", "v")
	out := BuildFrontmatter(mapping, "")
	if out != "---\nk: v\n---\n" {
		t.Errorf("empty-body output mismatch: %q", out)
	}
}

func TestBuildFrontmatter_NestedBodyPreserved(t *testing.T) {
	// Body itself contains another frontmatter — must NOT be parsed/rewritten.
	mapping := NewMapping()
	AppendScalarPair(mapping, "outer", "1")
	innerBody := "\n---\ninner: 2\n---\ninner body content"
	out := BuildFrontmatter(mapping, innerBody)

	// Parse outer back, body must equal innerBody exactly.
	_, gotBody, ok := ParseFrontmatter(out)
	if !ok {
		t.Fatal("ParseFrontmatter failed on nested build")
	}
	if gotBody != innerBody {
		t.Errorf("nested body not preserved verbatim:\nwant: %q\ngot:  %q", innerBody, gotBody)
	}
	// Inner can ALSO be parsed by re-running ParseFrontmatter on the body.
	innerMapping, innerInnerBody, ok := ParseFrontmatter(strings.TrimLeft(gotBody, "\n"))
	if !ok {
		t.Fatal("inner ParseFrontmatter failed")
	}
	if got := LookupScalar(innerMapping, "inner"); got != "2" {
		t.Errorf("inner.inner = %q", got)
	}
	if innerInnerBody != "inner body content" {
		t.Errorf("inner body = %q", innerInnerBody)
	}
}

func TestBuildFrontmatter_MultiLineScalarSurvivesRoundTrip(t *testing.T) {
	mapping := NewMapping()
	AppendScalarPair(mapping, "key", "v")
	multiline := "first line\nsecond line\nthird: with colon\nMUST NOT: leak as a top-level key"
	if err := AppendValue(mapping, "action", multiline); err != nil {
		t.Fatalf("AppendValue: %v", err)
	}
	out := BuildFrontmatter(mapping, "body")

	parsed, body, ok := ParseFrontmatter(out)
	if !ok {
		t.Fatalf("round-trip failed: %s", out)
	}
	if body != "body" {
		t.Errorf("body = %q", body)
	}
	if got := LookupScalar(parsed, "action"); got != multiline {
		t.Errorf("action multi-line did not round-trip:\nwant: %q\ngot:  %q", multiline, got)
	}
	// Confirm multi-line content did not leak as a top-level key.
	if got := LookupScalar(parsed, "MUST NOT"); got != "" {
		t.Errorf("MUST NOT must not be a top-level key after round-trip, got %q", got)
	}
}

func TestBuildFrontmatter_NilMapping(t *testing.T) {
	out := BuildFrontmatter(nil, "body")
	if out != "---\n---\nbody" {
		t.Errorf("nil mapping should yield empty fence, got: %q", out)
	}
}

// ---------- DropKeys ----------

func TestDropKeys(t *testing.T) {
	mapping := NewMapping()
	AppendScalarPair(mapping, "keep1", "a")
	AppendScalarPair(mapping, "drop1", "b")
	AppendScalarPair(mapping, "keep2", "c")
	AppendScalarPair(mapping, "drop2", "d")

	DropKeys(mapping, map[string]bool{"drop1": true, "drop2": true})

	if len(mapping.Content) != 4 {
		t.Fatalf("expected 4 content entries (2 pairs), got %d", len(mapping.Content))
	}
	if got := LookupScalar(mapping, "keep1"); got != "a" {
		t.Errorf("keep1 = %q", got)
	}
	if got := LookupScalar(mapping, "keep2"); got != "c" {
		t.Errorf("keep2 = %q", got)
	}
	if got := LookupScalar(mapping, "drop1"); got != "" {
		t.Errorf("drop1 should be removed, got %q", got)
	}
}

func TestDropKeys_PreservesOrder(t *testing.T) {
	mapping := NewMapping()
	AppendScalarPair(mapping, "a", "1")
	AppendScalarPair(mapping, "x", "drop")
	AppendScalarPair(mapping, "b", "2")
	AppendScalarPair(mapping, "y", "drop")
	AppendScalarPair(mapping, "c", "3")

	DropKeys(mapping, map[string]bool{"x": true, "y": true})

	out := BuildFrontmatter(mapping, "")
	aIdx := strings.Index(out, "a:")
	bIdx := strings.Index(out, "b:")
	cIdx := strings.Index(out, "c:")
	if !(aIdx < bIdx && bIdx < cIdx) {
		t.Errorf("relative order a<b<c not preserved: %s", out)
	}
}

// ---------- AppendValue / AppendScalarPair (typed values) ----------

func TestAppendValue_NativeTypes(t *testing.T) {
	mapping := NewMapping()
	if err := AppendValue(mapping, "flag", true); err != nil {
		t.Fatal(err)
	}
	if err := AppendValue(mapping, "count", 42); err != nil {
		t.Fatal(err)
	}
	if err := AppendValue(mapping, "ratio", 0.75); err != nil {
		t.Fatal(err)
	}
	if err := AppendValue(mapping, "name", "alice"); err != nil {
		t.Fatal(err)
	}
	out := BuildFrontmatter(mapping, "")

	// Native YAML types should emit unquoted.
	for _, want := range []string{"flag: true", "count: 42", "ratio: 0.75", "name: alice"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q in:\n%s", want, out)
		}
	}
}

func TestAppendScalarPair_NumericString(t *testing.T) {
	// AppendScalarPair stores raw strings; yaml.Marshal infers type. "true"
	// emits as bare bool; "42" as bare int.
	mapping := NewMapping()
	AppendScalarPair(mapping, "compressed", "true")
	AppendScalarPair(mapping, "original", "4126")
	out := BuildFrontmatter(mapping, "")
	if !strings.Contains(out, "compressed: true\n") {
		t.Errorf("compressed should emit as bare bool: %s", out)
	}
	if !strings.Contains(out, "original: 4126\n") {
		t.Errorf("original should emit as bare int: %s", out)
	}
}

// ---------- ExtractFrontmatterValue / Lookup ----------

func TestExtractFrontmatterValue(t *testing.T) {
	yamlBlock := "foo: bar\nbaz: qux"
	if got := ExtractFrontmatterValue(yamlBlock, "foo"); got != "bar" {
		t.Errorf("foo = %q", got)
	}
	if got := ExtractFrontmatterValue(yamlBlock, "missing"); got != "" {
		t.Errorf("missing key should yield empty string, got %q", got)
	}
}

func TestExtractFrontmatterValue_NotFooledByBlockScalarContent(t *testing.T) {
	// Original bug: line-based parser saw "    injected: true" inside an
	// action block scalar and mistakenly returned "true" for key "injected".
	yamlBlock := "sender: system\n" +
		"action: |-\n" +
		"    do something\n" +
		"    injected: true   <-- this is content of action, not a key\n"
	if got := ExtractFrontmatterValue(yamlBlock, "injected"); got != "" {
		t.Errorf("injected must NOT match block scalar content, got %q", got)
	}
	if got := ExtractFrontmatterValue(yamlBlock, "sender"); got != "system" {
		t.Errorf("sender = %q", got)
	}
}

func TestExtractFrontmatterValueFromContent(t *testing.T) {
	content := "---\nkey: val\n---\nbody"
	if got := ExtractFrontmatterValueFromContent(content, "key"); got != "val" {
		t.Errorf("key = %q", got)
	}
	if got := ExtractFrontmatterValueFromContent("no frontmatter", "key"); got != "" {
		t.Errorf("missing frontmatter should yield empty, got %q", got)
	}
}

// ---------- IsInjectedUserMessage ----------

func TestIsInjectedUserMessage(t *testing.T) {
	yes := "---\nsender: user\ninjected: true\n---\nhi"
	no := "---\nsender: user\n---\nhi"
	noFrontmatter := "plain message"

	if !IsInjectedUserMessage(yes) {
		t.Error("expected true for `injected: true` frontmatter")
	}
	if IsInjectedUserMessage(no) {
		t.Error("expected false when injected key is absent")
	}
	if IsInjectedUserMessage(noFrontmatter) {
		t.Error("expected false for content without frontmatter")
	}
}

func TestIsInjectedUserMessage_NotFooledByBlockScalar(t *testing.T) {
	// Same bug class: action body containing `injected: true` must not flip
	// the flag.
	content := "---\n" +
		"sender: user\n" +
		"action: |-\n" +
		"    injected: true\n" +
		"---\n" +
		"hi"
	if IsInjectedUserMessage(content) {
		t.Error("block scalar content must not be misread as the injected flag")
	}
}

// ---------- HasFrontmatterKeyValue ----------

func TestHasFrontmatterKeyValue(t *testing.T) {
	content := "---\nstatus: error\ntool: foo\n---\nError: bad"
	if !HasFrontmatterKeyValue(content, "status", "error") {
		t.Error("expected status=error to match")
	}
	if HasFrontmatterKeyValue(content, "status", "ok") {
		t.Error("status=ok should NOT match")
	}
	if HasFrontmatterKeyValue("not frontmatter", "status", "error") {
		t.Error("non-frontmatter input should yield false")
	}
}

// ---------- SortedFieldsMapping ----------

func TestSortedFieldsMapping(t *testing.T) {
	mapping, err := SortedFieldsMapping(
		[][2]string{{"command", "foo"}, {"status", "ok"}},
		map[string]any{"zeta": 1, "alpha": "a", "middle": true},
	)
	if err != nil {
		t.Fatal(err)
	}
	out := BuildFrontmatter(mapping, "")

	// Verify leading order: command then status.
	cmdIdx := strings.Index(out, "command:")
	stsIdx := strings.Index(out, "status:")
	if !(cmdIdx >= 0 && cmdIdx < stsIdx) {
		t.Errorf("expected `command:` before `status:`, got:\n%s", out)
	}
	// Verify extras sorted alphabetically.
	aIdx := strings.Index(out, "alpha:")
	mIdx := strings.Index(out, "middle:")
	zIdx := strings.Index(out, "zeta:")
	if !(stsIdx < aIdx && aIdx < mIdx && mIdx < zIdx) {
		t.Errorf("extras not sorted alphabetically after leading pairs:\n%s", out)
	}
	// Verify native types.
	if !strings.Contains(out, "middle: true") {
		t.Errorf("bool extra should emit unquoted: %s", out)
	}
	if !strings.Contains(out, "zeta: 1") {
		t.Errorf("int extra should emit unquoted: %s", out)
	}
}

func TestSortedFieldsMapping_Determinism(t *testing.T) {
	// Run several times with a non-trivially-sized map; output must be stable
	// (Go map iteration order is randomized, so any non-determinism leaks).
	extras := map[string]any{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7}
	first := ""
	for i := 0; i < 50; i++ {
		mapping, err := SortedFieldsMapping([][2]string{{"command", "x"}}, extras)
		if err != nil {
			t.Fatal(err)
		}
		out := BuildFrontmatter(mapping, "")
		if i == 0 {
			first = out
			continue
		}
		if out != first {
			t.Fatalf("non-deterministic output on iteration %d:\nfirst: %q\nthis:  %q", i, first, out)
		}
	}
}

// ---------- BuildFrontmatter idempotency ----------

func TestBuildFrontmatter_RoundTripIdempotent(t *testing.T) {
	mapping := NewMapping()
	AppendScalarPair(mapping, "k", "v")
	multiline := "line one\nline two with: colon\nthird"
	if err := AppendValue(mapping, "action", multiline); err != nil {
		t.Fatal(err)
	}
	out1 := BuildFrontmatter(mapping, "\nbody")

	parsed, body, ok := ParseFrontmatter(out1)
	if !ok {
		t.Fatal("parse failed")
	}
	out2 := BuildFrontmatter(parsed, body)
	if out1 != out2 {
		t.Errorf("not idempotent across parse/build:\nout1: %q\nout2: %q", out1, out2)
	}
}

// ---------- EncodeMapping (struct-to-mapping path) ----------

type sampleHeader struct {
	Name  string `yaml:"name"`
	Count int    `yaml:"count"`
	Skip  bool   `yaml:"skip,omitempty"`
}

func TestEncodeMapping(t *testing.T) {
	mapping, ok := EncodeMapping(sampleHeader{Name: "foo", Count: 7})
	if !ok {
		t.Fatal("EncodeMapping failed")
	}
	out := BuildFrontmatter(mapping, "")
	for _, want := range []string{"name: foo", "count: 7"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "skip:") {
		t.Errorf("omitempty bool should be omitted: %s", out)
	}
}

// ---------- Real-world fixture: Discord wake YAML ----------

func TestSplitFrontmatter_DiscordWakeShape(t *testing.T) {
	// Shape derived from the production Discord session that triggered the
	// original multi-line action bug.
	content := "---\n" +
		"source: session\n" +
		"thread: thread-f03389b8\n" +
		"session: discord:1480577226356559992\n" +
		"sender: system\n" +
		"caller_session_key: discord:1480577226356559992:threads:foo\n" +
		"action: |-\n" +
		"    Another session woke you. caller_session_key = the IMMEDIATE sender.\n" +
		"\n" +
		"    End this turn with exactly one of:\n" +
		"    1. dispatch(to=caller) — reply to the waker.\n" +
		"    2. dispatch(to=user) — redirect to your own channel user.\n" +
		"    MUST NOT: use dispatch({}) when you suspect mis-routing.\n" +
		"---\n" +
		"actual body content"

	mapping, body, ok := ParseFrontmatter(content)
	if !ok {
		t.Fatalf("ParseFrontmatter failed for Discord wake shape")
	}
	if body != "actual body content" {
		t.Errorf("body = %q", body)
	}
	if got := LookupScalar(mapping, "sender"); got != "system" {
		t.Errorf("sender = %q", got)
	}
	if got := LookupScalar(mapping, "caller_session_key"); got != "discord:1480577226356559992:threads:foo" {
		t.Errorf("caller_session_key = %q", got)
	}
	action := LookupScalar(mapping, "action")
	if !strings.Contains(action, "Another session woke you") {
		t.Errorf("action body lost: %q", action)
	}
	// MUST NOT must NOT be a top-level key (the original bug).
	if got := LookupScalar(mapping, "MUST NOT"); got != "" {
		t.Errorf("MUST NOT leaked as top-level key: %q", got)
	}
	// Round-trip via BuildFrontmatter must produce a parseable result with
	// the same scalar contents.
	rebuilt := BuildFrontmatter(mapping, body)
	parsed2, body2, ok2 := ParseFrontmatter(rebuilt)
	if !ok2 {
		t.Fatalf("round-trip parse failed: %s", rebuilt)
	}
	if body2 != "actual body content" {
		t.Errorf("round-trip body = %q", body2)
	}
	if got := LookupScalar(parsed2, "action"); got != action {
		t.Errorf("round-trip action mismatch")
	}
}

// ---------- yaml.Node compatibility ----------

func TestNewMapping_IsValidNode(t *testing.T) {
	m := NewMapping()
	if m.Kind != yaml.MappingNode {
		t.Errorf("NewMapping should return MappingNode, got Kind=%d", m.Kind)
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("yaml.Marshal of empty mapping: %v", err)
	}
	// Empty mapping serializes as "{}\n".
	if strings.TrimSpace(string(out)) != "{}" {
		t.Errorf("empty mapping marshal = %q, want '{}'", out)
	}
}
