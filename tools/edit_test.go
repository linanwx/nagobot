package tools

import (
	"testing"
)

func TestNormalizeTrailingWS(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{"hello   ", "hello"},
		{"hello\t\t", "hello"},
		{"hello   \nworld  \n", "hello\nworld\n"},
		{"  hello  ", "  hello"}, // leading preserved, trailing stripped
	}
	for _, tt := range tests {
		got := normalizeTrailingWS(tt.in)
		if got != tt.want {
			t.Errorf("normalizeTrailingWS(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormToOrigPos(t *testing.T) {
	// Original: "abc   \ndef  \nghi"
	// Normalized: "abc\ndef\nghi"
	origLines := []string{"abc   ", "def  ", "ghi"}
	normLines := []string{"abc", "def", "ghi"}

	tests := []struct {
		normPos  int
		wantOrig int
	}{
		{0, 0},   // 'a'
		{3, 3},   // end of "abc" in norm → before trailing spaces in orig
		{4, 7},   // 'd' in norm → 'd' in orig (after "abc   \n")
		{7, 10},  // end of "def" in norm → before trailing spaces in orig
		{8, 13},  // 'g' in norm → 'g' in orig (after "abc   \ndef  \n")
		{11, 16}, // end of "ghi"
	}
	for _, tt := range tests {
		got := normToOrigPos(origLines, normLines, tt.normPos)
		if got != tt.wantOrig {
			t.Errorf("normToOrigPos(normPos=%d) = %d, want %d", tt.normPos, got, tt.wantOrig)
		}
	}
}

func TestNormalizedReplace(t *testing.T) {
	// File has trailing spaces on lines, LLM's old_text doesn't.
	content := "func hello() {  \n\treturn 1  \n}\n"
	normOld := "func hello() {\n\treturn 1\n}"
	newText := "func hello() {\n\treturn 2\n}"

	got := normalizedReplace(content, normOld, newText)
	want := "func hello() {\n\treturn 2\n}\n"
	if got != want {
		t.Errorf("normalizedReplace:\n got: %q\nwant: %q", got, want)
	}
}

func TestNormalizedReplaceSingleLine(t *testing.T) {
	content := "x = 1  \ny = 2\n"
	normOld := "x = 1"
	newText := "x = 99"

	got := normalizedReplace(content, normOld, newText)
	want := "x = 99  \ny = 2\n"
	if got != want {
		t.Errorf("normalizedReplace single:\n got: %q\nwant: %q", got, want)
	}
}

func TestNormalizedReplaceAll(t *testing.T) {
	content := "x = 1  \ny = 2\nx = 1  \n"
	normOld := "x = 1"
	newText := "x = 99"

	got := normalizedReplaceAll(content, normOld, newText)
	want := "x = 99  \ny = 2\nx = 99  \n"
	if got != want {
		t.Errorf("normalizedReplaceAll:\n got: %q\nwant: %q", got, want)
	}
}

func TestNormalizedReplaceMultiLine(t *testing.T) {
	// Multi-line match where intermediate lines have trailing whitespace.
	content := "if true {  \n\tx = 1  \n\ty = 2  \n}\n"
	normOld := "if true {\n\tx = 1\n\ty = 2\n}"
	newText := "if true {\n\tx = 10\n\ty = 20\n}"

	got := normalizedReplace(content, normOld, newText)
	want := "if true {\n\tx = 10\n\ty = 20\n}\n"
	if got != want {
		t.Errorf("normalizedReplace multiline:\n got: %q\nwant: %q", got, want)
	}
}

func TestNormalizedReplaceNoMatch(t *testing.T) {
	content := "hello world\n"
	normOld := "goodbye"
	got := normalizedReplace(content, normOld, "hi")
	if got != content {
		t.Errorf("expected no change, got: %q", got)
	}
}
