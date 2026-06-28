package tools

import (
	"strings"
	"testing"
)

func TestNormalizeForFuzzyMatch(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"trailing   ", "trailing"},
		{"don’t", "don't"},       // smart apostrophe
		{"“quote”", "\"quote\""}, // smart double quotes
		{"a—b", "a-b"},           // em-dash
		{"a–b", "a-b"},           // en-dash
		{"a−b", "a-b"},           // minus sign
		{"a\u00A0b", "a b"},      // NBSP
		{"a\u3000b", "a b"},      // ideographic space
		{"ＡＢＣ", "ABC"},           // full-width -> NFKC
		{"x  \ny  ", "x\ny"},     // per-line trailing strip
	}
	for _, tt := range tests {
		if got := normalizeForFuzzyMatch(tt.in); got != tt.want {
			t.Errorf("normalizeForFuzzyMatch(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDetectLineEnding(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\r\nb", "\r\n"},
		{"a\nb", "\n"},
		{"noeol", "\n"},
		{"a\nb\r\nc", "\n"}, // first ending wins
	}
	for _, c := range cases {
		if got := detectLineEnding(c.in); got != c.want {
			t.Errorf("detectLineEnding(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStripBom(t *testing.T) {
	if bom, text := stripBom("\uFEFFhello"); bom != "\uFEFF" || text != "hello" {
		t.Errorf("stripBom(bom) = %q,%q", bom, text)
	}
	if bom, text := stripBom("hello"); bom != "" || text != "hello" {
		t.Errorf("stripBom(no bom) = %q,%q", bom, text)
	}
}

func applyOne(t *testing.T, content string, edits ...editPair) (string, bool, error) {
	t.Helper()
	return applyEditsToNormalizedContent(content, edits, "test.txt")
}

func TestApplyEdits_ExactSingle(t *testing.T) {
	got, fuzzy, err := applyOne(t, "x = 1\n", editPair{"x = 1", "x = 2"})
	if err != nil {
		t.Fatal(err)
	}
	if fuzzy {
		t.Error("expected non-fuzzy match")
	}
	if got != "x = 2\n" {
		t.Errorf("got %q", got)
	}
}

func TestApplyEdits_Multi(t *testing.T) {
	got, _, err := applyOne(t, "a=1\nb=2\nc=3\n", editPair{"a=1", "a=10"}, editPair{"c=3", "c=30"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "a=10\nb=2\nc=30\n" {
		t.Errorf("got %q", got)
	}
}

func TestApplyEdits_FuzzySmartQuotes(t *testing.T) {
	got, fuzzy, err := applyOne(t, "x = “hello”\n", editPair{`x = "hello"`, `x = "world"`})
	if err != nil {
		t.Fatal(err)
	}
	if !fuzzy {
		t.Error("expected fuzzy match")
	}
	if got != "x = \"world\"\n" {
		t.Errorf("got %q", got)
	}
}

func TestApplyEdits_FuzzyPreservesUntouchedLines(t *testing.T) {
	// Edit only the first line; the second line's smart quotes must survive byte-for-byte.
	content := "title = “A”\nname = “B”\n"
	got, fuzzy, err := applyOne(t, content, editPair{`title = "A"`, `title = "C"`})
	if err != nil {
		t.Fatal(err)
	}
	if !fuzzy {
		t.Error("expected fuzzy match")
	}
	want := "title = \"C\"\nname = “B”\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestApplyEdits_TrailingWhitespaceFuzzy(t *testing.T) {
	content := "func f() {  \n\treturn 1  \n}\n"
	got, fuzzy, err := applyOne(t, content, editPair{"func f() {\n\treturn 1\n}", "func f() {\n\treturn 2\n}"})
	if err != nil {
		t.Fatal(err)
	}
	if !fuzzy {
		t.Error("expected fuzzy match")
	}
	if got != "func f() {\n\treturn 2\n}\n" {
		t.Errorf("got %q", got)
	}
}

func TestApplyEdits_Overlap(t *testing.T) {
	_, _, err := applyOne(t, "abcdef\n", editPair{"abc", "X"}, editPair{"bcd", "Y"})
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Errorf("expected overlap error, got %v", err)
	}
}

func TestApplyEdits_Duplicate(t *testing.T) {
	_, _, err := applyOne(t, "x x x\n", editPair{"x", "y"})
	if err == nil || !strings.Contains(err.Error(), "occurrences") {
		t.Errorf("expected duplicate error, got %v", err)
	}
}

func TestApplyEdits_NotFound(t *testing.T) {
	_, _, err := applyOne(t, "abc\n", editPair{"xyz", "q"})
	if err == nil || !strings.Contains(err.Error(), "could not find") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestApplyEdits_NoChange(t *testing.T) {
	_, _, err := applyOne(t, "abc\n", editPair{"abc", "abc"})
	if err == nil || !strings.Contains(err.Error(), "no changes") {
		t.Errorf("expected no-change error, got %v", err)
	}
}

func TestApplyEdits_EmptyOldText(t *testing.T) {
	_, _, err := applyOne(t, "abc\n", editPair{"", "x"})
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("expected empty-old_text error, got %v", err)
	}
}

func TestLineEndingRoundTrip(t *testing.T) {
	orig := "a\r\nb\r\nc"
	ending := detectLineEnding(orig)
	lf := normalizeToLF(orig)
	if lf != "a\nb\nc" {
		t.Fatalf("normalizeToLF = %q", lf)
	}
	if got := restoreLineEndings(lf, ending); got != orig {
		t.Errorf("restoreLineEndings = %q, want %q", got, orig)
	}
}
