package tgmd

import (
	"strings"
	"testing"
)

func TestBasicText(t *testing.T) {
	got := Convert("Hello world")
	expect(t, got, "Hello world")
}

func TestBold(t *testing.T) {
	got := Convert("Hello **world**")
	expect(t, got, "Hello <b>world</b>")
}

func TestItalic(t *testing.T) {
	got := Convert("Hello *world*")
	expect(t, got, "Hello <i>world</i>")
}

func TestBoldItalic(t *testing.T) {
	got := Convert("Hello ***world***")
	// Goldmark nests emphasis; order may vary.
	if !strings.Contains(got, "<b>") || !strings.Contains(got, "<i>") {
		t.Errorf("expected bold+italic tags, got: %q", got)
	}
}

func TestStrikethrough(t *testing.T) {
	got := Convert("Hello ~~world~~")
	expect(t, got, "Hello <s>world</s>")
}

func TestHeadings(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"# Title", "<b>Title</b>"},
		{"## Subtitle", "<b>Subtitle</b>"},
		{"### Section", "<b>Section</b>"},
	}
	for _, tt := range tests {
		got := Convert(tt.in)
		expect(t, got, tt.want)
	}
}

func TestInlineCode(t *testing.T) {
	got := Convert("Use `fmt.Println`")
	expect(t, got, "Use <code>fmt.Println</code>")
}

func TestFencedCodeBlock(t *testing.T) {
	md := "```go\nfmt.Println(\"hello\")\n```"
	got := Convert(md)
	if !strings.Contains(got, "<pre><code class=\"language-go\">") {
		t.Errorf("missing language class, got: %q", got)
	}
	if !strings.Contains(got, "fmt.Println") {
		t.Errorf("missing code content, got: %q", got)
	}
	if !strings.Contains(got, "</code></pre>") {
		t.Errorf("missing closing tags, got: %q", got)
	}
}

func TestCodeBlockEscaping(t *testing.T) {
	md := "```\na < b && c > d\n```"
	got := Convert(md)
	if !strings.Contains(got, "a &lt; b &amp;&amp; c &gt; d") {
		t.Errorf("HTML not escaped in code block, got: %q", got)
	}
}

func TestLink(t *testing.T) {
	got := Convert("[Google](https://google.com)")
	expect(t, got, `<a href="https://google.com">Google</a>`)
}

func TestImage(t *testing.T) {
	got := Convert("![alt text](https://example.com/img.png)")
	if !strings.Contains(got, `<a href="https://example.com/img.png">`) {
		t.Errorf("image not converted to link, got: %q", got)
	}
}

func TestUnorderedList(t *testing.T) {
	md := "- item 1\n- item 2\n- item 3"
	got := Convert(md)
	if !strings.Contains(got, "\u2022 item 1") {
		t.Errorf("missing bullet item 1, got: %q", got)
	}
	if !strings.Contains(got, "\u2022 item 3") {
		t.Errorf("missing bullet item 3, got: %q", got)
	}
}

func TestOrderedList(t *testing.T) {
	md := "1. first\n2. second\n3. third"
	got := Convert(md)
	if !strings.Contains(got, "1. first") {
		t.Errorf("missing ordered item, got: %q", got)
	}
	if !strings.Contains(got, "3. third") {
		t.Errorf("missing ordered item, got: %q", got)
	}
}

func TestNestedList(t *testing.T) {
	md := "- item 1\n  - sub 1\n  - sub 2\n- item 2"
	got := Convert(md)
	if !strings.Contains(got, "\u2022 item 1") {
		t.Errorf("missing outer item, got: %q", got)
	}
	if !strings.Contains(got, "  \u2022 sub 1") {
		t.Errorf("missing nested item, got: %q", got)
	}
}

func TestBlockquote(t *testing.T) {
	got := Convert("> Hello world")
	if !strings.Contains(got, "<blockquote>") || !strings.Contains(got, "</blockquote>") {
		t.Errorf("missing blockquote tags, got: %q", got)
	}
	if !strings.Contains(got, "Hello world") {
		t.Errorf("missing blockquote content, got: %q", got)
	}
}

func TestThematicBreak(t *testing.T) {
	got := Convert("---")
	if !strings.Contains(got, "——————————") {
		t.Errorf("missing thematic break, got: %q", got)
	}
}

func TestHTMLEscaping(t *testing.T) {
	got := Convert("a < b & c > d")
	if !strings.Contains(got, "&lt;") || !strings.Contains(got, "&amp;") || !strings.Contains(got, "&gt;") {
		t.Errorf("HTML entities not escaped, got: %q", got)
	}
}

func TestTable(t *testing.T) {
	md := "| Name | Age |\n|------|-----|\n| Alice | 30 |\n| Bob | 25 |"
	got := Convert(md)
	if strings.Contains(got, "<pre>") {
		t.Errorf("table should be rendered as list, got: %q", got)
	}
	if !strings.Contains(got, "Alice") || !strings.Contains(got, "Bob") {
		t.Errorf("missing table data, got: %q", got)
	}
	if !strings.Contains(got, "<b>1.</b>") {
		t.Errorf("missing row index, got: %q", got)
	}
	if !strings.Contains(got, "• <b>Name</b>: Alice") || !strings.Contains(got, "• <b>Age</b>: 30") {
		t.Errorf("missing row fields, got: %q", got)
	}
	t.Logf("Table output:\n%s", got)
}

func TestTableCJK(t *testing.T) {
	md := "| 名前 | 年齢 |\n|------|------|\n| 太郎 | 30 |"
	got := Convert(md)
	if !strings.Contains(got, "太郎") {
		t.Errorf("missing CJK content, got: %q", got)
	}
	t.Logf("CJK table output:\n%s", got)
}

func TestTaskList(t *testing.T) {
	md := "- [x] Done\n- [ ] Todo"
	got := Convert(md)
	if !strings.Contains(got, "\u2705") { // ✅
		t.Errorf("missing checked checkbox, got: %q", got)
	}
	if !strings.Contains(got, "\u2610") { // ☐
		t.Errorf("missing unchecked checkbox, got: %q", got)
	}
}

func TestComplex(t *testing.T) {
	md := `# Report

## Summary

This is a **bold** and *italic* test with ` + "`inline code`" + `.

### Data

| Item  | Count |
|-------|-------|
| Alpha | 100   |
| Beta  | 200   |

### Steps

1. First step
2. Second step
   - Sub item a
   - Sub item b
3. Third step

> Important note here

---

` + "```python\nprint('hello')\n```"

	got := Convert(md)

	checks := []string{
		"<b>Report</b>",
		"<b>Summary</b>",
		"<b>bold</b>",
		"<i>italic</i>",
		"<code>inline code</code>",
		"<b>Data</b>",
		"• <b>Item</b>: Alpha",
		"Alpha",
		"1. First step",
		"<blockquote>",
		"——————————",
		"<pre><code class=\"language-python\">",
	}

	for _, c := range checks {
		if !strings.Contains(got, c) {
			t.Errorf("missing %q in output", c)
		}
	}

	t.Logf("Complex output:\n%s", got)
}

// ---------------------------------------------------------------------------

func expect(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}
