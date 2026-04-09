package agent

import "testing"

func TestParseTokenAmount(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"   ", 0},
		{"0", 0},
		{"64k", 64000},
		{"64K", 64000},
		{"200k", 200000},
		{"1M", 1000000},
		{"1m", 1000000},
		{"2M", 2000000},
		{"200000", 200000},
		{"200_000", 200000},
		{"200,000", 200000},
		{"  64k  ", 64000},
		{"abc", 0},
		{"k", 0},
		{"-5k", 0},
	}
	for _, c := range cases {
		got := ParseTokenAmount(c.in)
		if got != c.want {
			t.Errorf("ParseTokenAmount(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseTemplateContextWindowCap(t *testing.T) {
	tpl := `---
name: rephrase
context_window_cap: 64k
---
body`
	meta, _, hasHeader, err := ParseTemplate(tpl)
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	if !hasHeader {
		t.Fatal("expected frontmatter header")
	}
	if meta.ContextWindowCap != "64k" {
		t.Errorf("ContextWindowCap = %q, want %q", meta.ContextWindowCap, "64k")
	}
	if got := ParseTokenAmount(meta.ContextWindowCap); got != 64000 {
		t.Errorf("parsed cap = %d, want 64000", got)
	}
}
