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

func TestParseTemplateDisableTools(t *testing.T) {
	on, _, _, err := ParseTemplate("---\nname: pre-think\ndisable_tools: true\n---\nbody")
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	if !on.DisableTools {
		t.Error("disable_tools: true should parse to DisableTools == true")
	}

	// Absent → false (default; agents keep their tools).
	off, _, _, err := ParseTemplate("---\nname: soul\n---\nbody")
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	if off.DisableTools {
		t.Error("DisableTools should default to false when the field is absent")
	}
}

func TestParseTemplate_Specialty(t *testing.T) {
	cases := []struct {
		name string
		fm   string
		want []string
	}{
		{"array", "---\nname: a\nspecialty: [pdf, toolcall]\n---\nbody", []string{"pdf", "toolcall"}},
		{"single-array", "---\nname: a\nspecialty: [pdf]\n---\nbody", []string{"pdf"}},
		{"scalar (lenient)", "---\nname: a\nspecialty: pdf\n---\nbody", []string{"pdf"}},
		{"empty", "---\nname: a\n---\nbody", nil},
		{"blank scalar", "---\nname: a\nspecialty: \n---\nbody", nil},
		{"trims whitespace", "---\nname: a\nspecialty: [ cron , toolcall ]\n---\nbody", []string{"cron", "toolcall"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			meta, _, _, err := ParseTemplate(c.fm)
			if err != nil {
				t.Fatalf("ParseTemplate: %v", err)
			}
			got := []string(meta.Specialties)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}
