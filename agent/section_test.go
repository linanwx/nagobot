package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeadingLevel(t *testing.T) {
	tests := []struct {
		line string
		want int
	}{
		{"# Foo", 1},
		{"## Foo", 2},
		{"### Foo Bar", 3},
		{"###### Deep", 6},
		{"#NotAHeading", 0},
		{"plain text", 0},
		{"   ## Indented", 2},
		{"####### TooDeep", 0},
		{"", 0},
		{"# ", 1},
	}
	for _, tt := range tests {
		if got := headingLevel(tt.line); got != tt.want {
			t.Errorf("headingLevel(%q) = %d, want %d", tt.line, got, tt.want)
		}
	}
}

func TestIsCodeFenceLine(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"```", true},
		{"```go", true},
		{"~~~", true},
		{"  ```", true},
		{"not a fence", false},
		{"``not enough", false},
	}
	for _, tt := range tests {
		if got := isCodeFenceLine(tt.line); got != tt.want {
			t.Errorf("isCodeFenceLine(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestNormalizeHeadings(t *testing.T) {
	tests := []struct {
		name    string
		content string
		target  int
		want    string
	}{
		{
			name:    "shift H1 to H3",
			content: "# Foo\n## Bar\ntext",
			target:  3,
			want:    "### Foo\n#### Bar\ntext",
		},
		{
			name:    "no headings",
			content: "just text\nmore text",
			target:  2,
			want:    "just text\nmore text",
		},
		{
			name:    "already at target",
			content: "## Foo\n### Bar",
			target:  2,
			want:    "## Foo\n### Bar",
		},
		{
			name:    "code fence protection",
			content: "# Real\n```\n# Not a heading\n```\n## Also Real",
			target:  2,
			want:    "## Real\n```\n# Not a heading\n```\n### Also Real",
		},
		{
			name:    "cap at H6",
			content: "# A\n## B\n### C\n#### D\n##### E\n###### F",
			target:  3,
			want:    "### A\n#### B\n##### C\n###### D\n###### E\n###### F",
		},
		{
			name:    "empty content",
			content: "",
			target:  2,
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeHeadings(tt.content, tt.target)
			if got != tt.want {
				t.Errorf("normalizeHeadings(target=%d):\ngot:  %q\nwant: %q", tt.target, got, tt.want)
			}
		})
	}
}

func TestSectionRegistry_Assemble(t *testing.T) {
	dir := t.TempDir()

	writeSection(t, dir, "context.md", `---
name: context
priority: 100
---
# Context

Date: today`)

	writeSection(t, dir, "mechanism.md", `---
name: mechanism
priority: 200
---
# How it works

Channels and threads.`)

	writeSection(t, dir, "tools.md", `---
name: tools
priority: 400
parent: mechanism
---
# Tools

Available tools here.`)

	writeSection(t, dir, "web-search.md", `---
name: web-search
priority: 410
parent: tools
---
# Web Search Guide

## Sources
Source list here.`)

	reg := NewSectionRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatal(err)
	}
	if reg.Count() != 4 {
		t.Fatalf("Count() = %d, want 4", reg.Count())
	}

	result := reg.Assemble()

	// Root sections should be H1.
	if !strings.Contains(result, "# Context") {
		t.Error("root section 'context' should be H1")
	}
	if !strings.Contains(result, "# How it works") {
		t.Error("root section 'mechanism' should be H1")
	}

	// Children of mechanism should be H2.
	if !strings.Contains(result, "## Tools") {
		t.Error("child of mechanism 'tools' should be H2")
	}

	// Children of tools should be H3.
	if !strings.Contains(result, "### Web Search Guide") {
		t.Error("child of tools 'web-search' should be H3")
	}
	if !strings.Contains(result, "#### Sources") {
		t.Error("## Sources inside web-search should become H4")
	}

	// Verify order: context before mechanism.
	ctxIdx := strings.Index(result, "# Context")
	mechIdx := strings.Index(result, "# How it works")
	if ctxIdx > mechIdx {
		t.Error("context (priority 100) should appear before mechanism (priority 200)")
	}
}

func TestSectionRegistry_DanglingParent(t *testing.T) {
	dir := t.TempDir()

	writeSection(t, dir, "orphan.md", `---
name: orphan
priority: 100
parent: nonexistent
---
# Orphan Section

Content here.`)

	reg := NewSectionRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatal(err)
	}

	result := reg.Assemble()

	// Dangling parent should be treated as root.
	if !strings.Contains(result, "# Orphan Section") {
		t.Error("orphan with dangling parent should be rendered as root (H1)")
	}
}

func TestSectionRegistry_RealSections(t *testing.T) {
	dir := filepath.Join("..", "cmd", "templates", "system", "sections")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Skip("sections directory not yet created")
	}
	reg := NewSectionRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatal(err)
	}
	if reg.Count() < 8 {
		t.Errorf("expected at least 8 sections, got %d", reg.Count())
	}
	result := reg.Assemble()
	if !strings.Contains(result, "How nagobot works") {
		t.Error("assembled output should contain 'How nagobot works'")
	}
	if !strings.Contains(result, "{{TOOLS}}") {
		t.Error("assembled output should contain {{TOOLS}} placeholder")
	}
	t.Logf("assembled %d chars from %d sections", len(result), reg.Count())
}

func writeSection(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
