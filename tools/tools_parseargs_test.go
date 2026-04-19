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
