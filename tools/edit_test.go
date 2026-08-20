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

// An old_text that runs to EOF may carry more trailing newlines than the file
// has, because read_file terminates every rendered line with one. Reconciling
// that is what makes "append a line" expressible as a single replacement.
func TestApplyEdits_EOFTrailingNewlineTolerated(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		old     string
		new     string
		want    string
	}{
		{"extra newline on a file that ends with one", "a\nb\n", "b\n\n", "b\nc\n\n", "a\nb\nc\n"},
		{"newline on a file that ends without one", "a\nb", "b\n", "b\nc\n", "a\nb\nc"},
		{"deletion at EOF", "a\nb\n", "b\n\n", "", "a\n"},
	} {
		got, fuzzy, err := applyEditsToNormalizedContent(tc.content, []editPair{{oldText: tc.old, newText: tc.new}}, "f")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
		// The reconciled edit matches exactly, so it must not drag the
		// replacement into fuzzy-normalized space over a newline count.
		if fuzzy {
			t.Fatalf("%s: reconciliation must not report a fuzzy match", tc.name)
		}
	}
}

// The tolerance is anchored at EOF and nowhere else. An anchor that sits in the
// middle of the file carries no claim about the file's ending, so an extra
// newline there is a real mismatch and must still fail.
func TestApplyEdits_TrailingNewlineNotToleratedMidFile(t *testing.T) {
	_, _, err := applyEditsToNormalizedContent("a\nb\nc\n", []editPair{{oldText: "b\n\n", newText: "B\n\n"}}, "f")
	if err == nil {
		t.Fatal("a mid-file anchor with an invented newline must not match")
	}
}

// EOF anchoring picks the LAST occurrence. When the trimmed anchor appears more
// than once the model may have meant an earlier one, so it gets the ordinary
// error rather than a silent edit of the end of the file.
func TestApplyEdits_EOFReconcileRequiresUniqueAnchor(t *testing.T) {
	_, _, err := applyEditsToNormalizedContent("dup\ndup\n", []editPair{{oldText: "dup\n\n", newText: "dup\nx\n\n"}}, "f")
	if err == nil {
		t.Fatal("an ambiguous EOF anchor must not be reconciled")
	}
}

// Reconciliation rewrites the edit, not the file: bytes the edit never touched
// keep their original form, including the smart quotes fuzzy matching would
// have flattened.
func TestApplyEdits_EOFReconcilePreservesUntouchedBytes(t *testing.T) {
	content := "he said “hello”\nlast\n"
	got, _, err := applyEditsToNormalizedContent(content, []editPair{{oldText: "last\n\n", newText: "last\nmore\n\n"}}, "f")
	if err != nil {
		t.Fatal(err)
	}
	if want := "he said “hello”\nlast\nmore\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
