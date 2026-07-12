package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

type demoArgs struct {
	Query   string   `json:"query" required:"true" alias:"q,search"`
	Source  string   `json:"source,omitempty"`
	Limit   int      `json:"limit,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	NoValue string   `json:"no_value,omitempty" required:"true"`
}

type minimalArgs struct {
	Name string `json:"name" required:"true"`
}

func parse(args string, target any) string {
	raw := json.RawMessage(args)
	switch t := target.(type) {
	case *demoArgs:
		return parseArgs(raw, t)
	case *minimalArgs:
		return parseArgs(raw, t)
	}
	return "Error: unsupported test target"
}

func TestParseArgs_AliasRewritesToCanonical(t *testing.T) {
	var a demoArgs
	if out := parse(`{"q":"hello","no_value":"x"}`, &a); out != "" {
		t.Fatalf("unexpected error: %s", out)
	}
	if a.Query != "hello" {
		t.Fatalf("alias not applied: query=%q", a.Query)
	}
}

func TestParseArgs_CanonicalWinsOverAlias(t *testing.T) {
	var a demoArgs
	if out := parse(`{"query":"primary","q":"alias","no_value":"x"}`, &a); out != "" {
		t.Fatalf("unexpected error: %s", out)
	}
	if a.Query != "primary" {
		t.Fatalf("canonical should win: query=%q", a.Query)
	}
}

func TestParseArgs_MultipleAliasesAccepted(t *testing.T) {
	var a demoArgs
	if out := parse(`{"search":"via-search","no_value":"x"}`, &a); out != "" {
		t.Fatalf("unexpected error: %s", out)
	}
	if a.Query != "via-search" {
		t.Fatalf("second alias not applied: query=%q", a.Query)
	}
}

func TestParseArgs_UnknownKeyRejected(t *testing.T) {
	var a demoArgs
	out := parse(`{"query":"x","no_value":"x","bogus":1}`, &a)
	if !strings.Contains(out, "unknown argument") || !strings.Contains(out, "bogus") {
		t.Fatalf("expected unknown-argument rejection, got: %s", out)
	}
	if !strings.Contains(out, "allowed:") {
		t.Fatalf("expected allowed list in error, got: %s", out)
	}
}

func TestParseArgs_UnknownKeyRejectedAfterAliasRewrite(t *testing.T) {
	// "q" is a valid alias; "banana" is not. Error must mention banana only.
	var a demoArgs
	out := parse(`{"q":"x","no_value":"x","banana":true}`, &a)
	if !strings.Contains(out, "banana") || strings.Contains(out, "q ") {
		t.Fatalf("expected only banana to be rejected, got: %s", out)
	}
}

func TestParseArgs_MissingRequiredStringRejected(t *testing.T) {
	var a demoArgs
	out := parse(`{"no_value":"x"}`, &a)
	if !strings.Contains(out, "missing or empty required argument") || !strings.Contains(out, "query") {
		t.Fatalf("expected missing-required error, got: %s", out)
	}
}

func TestParseArgs_EmptyStringTreatedAsMissing(t *testing.T) {
	var a demoArgs
	out := parse(`{"query":"","no_value":"x"}`, &a)
	if !strings.Contains(out, "missing or empty") || !strings.Contains(out, "query") {
		t.Fatalf("expected empty-string to be rejected, got: %s", out)
	}
}

func TestParseArgs_MultipleMissingReported(t *testing.T) {
	var a demoArgs
	out := parse(`{}`, &a)
	if !strings.Contains(out, "no_value") || !strings.Contains(out, "query") {
		t.Fatalf("expected both missing fields in error, got: %s", out)
	}
}

func TestParseArgs_EmptyArgsTreatedAsObject(t *testing.T) {
	// Empty/null args should not panic; required check still fires.
	var a minimalArgs
	if out := parse(``, &a); !strings.Contains(out, "missing or empty") {
		t.Fatalf("empty args: expected missing error, got: %s", out)
	}
	if out := parse(`null`, &a); !strings.Contains(out, "missing or empty") {
		t.Fatalf("null args: expected missing error, got: %s", out)
	}
}

func TestParseArgs_HappyPathLeavesStructPopulated(t *testing.T) {
	var a demoArgs
	if out := parse(`{"query":"hi","no_value":"y","limit":5,"tags":["a","b"]}`, &a); out != "" {
		t.Fatalf("unexpected error: %s", out)
	}
	if a.Query != "hi" || a.NoValue != "y" || a.Limit != 5 || len(a.Tags) != 2 {
		t.Fatalf("unexpected result: %+v", a)
	}
}

// --- Recursion: the guards must reach inside nested objects and arrays -------
//
// A top-level-only check was the root cause of two silent-drop defects that
// shipped: dispatch(sends=[{delay:"1h"}]) fired immediately while the model
// believed it had scheduled a delayed wake, and edit_file(edits=[{replace_all:
// true}]) dropped the flag and failed on a confusing uniqueness error instead.

type nestedItem struct {
	Kind string `json:"kind" required:"true"`
	Text string `json:"text" alias:"body,content"`
}

type nestedArgs struct {
	Items []nestedItem `json:"items"`
	Inner *nestedItem  `json:"inner,omitempty"`
}

func parseNested(args string, target *nestedArgs) string {
	return parseArgs(json.RawMessage(args), target)
}

func TestParseArgs_UnknownKeyInsideArrayElementRejected(t *testing.T) {
	var a nestedArgs
	out := parseNested(`{"items":[{"kind":"a","text":"x"},{"kind":"b","delay":"1h"}]}`, &a)
	if !strings.Contains(out, "unknown argument") {
		t.Fatalf("expected nested unknown-key rejection, got: %s", out)
	}
	if !strings.Contains(out, "items[1].delay") {
		t.Fatalf("expected the JSON path items[1].delay in the error, got: %s", out)
	}
	if !strings.Contains(out, "allowed:") || !strings.Contains(out, "kind") {
		t.Fatalf("expected the allowed list of the nested shape, got: %s", out)
	}
}

func TestParseArgs_UnknownKeyInsideNestedObjectRejected(t *testing.T) {
	var a nestedArgs
	out := parseNested(`{"inner":{"kind":"a","bogus":1}}`, &a)
	if !strings.Contains(out, "inner.bogus") {
		t.Fatalf("expected the JSON path inner.bogus in the error, got: %s", out)
	}
}

func TestParseArgs_AliasRewrittenInsideArrayElement(t *testing.T) {
	var a nestedArgs
	if out := parseNested(`{"items":[{"kind":"a","body":"via-alias"}]}`, &a); out != "" {
		t.Fatalf("unexpected error: %s", out)
	}
	if len(a.Items) != 1 || a.Items[0].Text != "via-alias" {
		t.Fatalf("nested alias not applied: %+v", a.Items)
	}
}

func TestParseArgs_NestedValidPayloadAccepted(t *testing.T) {
	var a nestedArgs
	if out := parseNested(`{"items":[{"kind":"a","text":"x"},{"kind":"b","content":"y"}]}`, &a); out != "" {
		t.Fatalf("unexpected error: %s", out)
	}
	if len(a.Items) != 2 || a.Items[1].Text != "y" {
		t.Fatalf("unexpected result: %+v", a.Items)
	}
}

// --- Pointer fields: "required, but empty is legal" -------------------------

type pointerArgs struct {
	Path    string  `json:"path" required:"true"`
	Content *string `json:"content" required:"true"`
}

func TestParseArgs_RequiredPointerRejectsMissingAndNull(t *testing.T) {
	for _, args := range []string{`{"path":"p"}`, `{"path":"p","content":null}`} {
		var a pointerArgs
		out := parseArgs(json.RawMessage(args), &a)
		if !strings.Contains(out, "missing or empty required argument") || !strings.Contains(out, "content") {
			t.Fatalf("args %s: expected missing-content error, got: %s", args, out)
		}
	}
}

func TestParseArgs_RequiredPointerAcceptsEmptyString(t *testing.T) {
	var a pointerArgs
	if out := parseArgs(json.RawMessage(`{"path":"p","content":""}`), &a); out != "" {
		t.Fatalf("empty string must be accepted for a required pointer, got: %s", out)
	}
	if a.Content == nil || *a.Content != "" {
		t.Fatalf("expected non-nil empty content, got: %v", a.Content)
	}
}

// Type mismatches must stay loud and name the field — a model can recover from
// that, but not from a silently dropped value.
func TestParseArgs_TypeMismatchNamesTheField(t *testing.T) {
	var a demoArgs
	out := parse(`{"query":"x","no_value":"y","limit":"50"}`, &a)
	if !strings.Contains(out, "invalid arguments") || !strings.Contains(out, "limit") {
		t.Fatalf("expected a type error naming limit, got: %s", out)
	}
}
