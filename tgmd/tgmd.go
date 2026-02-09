// Package tgmd converts standard Markdown into Telegram-compatible HTML.
//
// Telegram's Bot API supports a limited subset of HTML. This package parses
// Markdown (including GFM tables, strikethrough, and task lists) and produces
// HTML that Telegram can render correctly.
//
// Unsupported Markdown features are mapped to approximations:
//   - Headings become bold text
//   - Tables become readable list blocks
//   - Images become links
//   - Horizontal rules become a line of em-dashes
package tgmd

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Convert converts standard Markdown text into Telegram-compatible HTML.
func Convert(markdown string) string {
	source := []byte(markdown)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := md.Parser().Parse(text.NewReader(source))

	r := &renderer{source: source}
	r.walkBlock(doc)
	return strings.TrimRight(r.buf.String(), "\n ")
}

type renderer struct {
	source    []byte
	buf       bytes.Buffer
	listDepth int
}

// ---------------------------------------------------------------------------
// HTML escaping
// ---------------------------------------------------------------------------

func escapeHTML(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Block-level rendering
// ---------------------------------------------------------------------------

func (r *renderer) walkBlock(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		r.block(c)
	}
}

func (r *renderer) block(node ast.Node) {
	switch n := node.(type) {
	case *ast.Document:
		r.walkBlock(n)

	case *ast.Heading:
		r.buf.WriteString("<b>")
		r.inlines(n)
		r.buf.WriteString("</b>\n\n")

	case *ast.Paragraph:
		r.inlines(n)
		r.buf.WriteString("\n\n")

	case *ast.TextBlock:
		r.inlines(n)
		r.buf.WriteString("\n")

	case *ast.Blockquote:
		r.buf.WriteString("<blockquote>")
		sub := &renderer{source: r.source}
		sub.walkBlock(n)
		r.buf.WriteString(strings.TrimRight(sub.buf.String(), "\n "))
		r.buf.WriteString("</blockquote>\n\n")

	case *ast.List:
		r.list(n)

	case *ast.ListItem:
		// Handled inside list(); fallback.
		r.walkBlock(n)

	case *ast.FencedCodeBlock:
		lang := string(n.Language(r.source))
		if lang != "" {
			fmt.Fprintf(&r.buf, "<pre><code class=\"language-%s\">", escapeHTML(lang))
		} else {
			r.buf.WriteString("<pre><code>")
		}
		r.writeLines(n)
		r.buf.WriteString("</code></pre>\n\n")

	case *ast.CodeBlock:
		r.buf.WriteString("<pre><code>")
		r.writeLines(n)
		r.buf.WriteString("</code></pre>\n\n")

	case *ast.ThematicBreak:
		r.buf.WriteString("——————————\n\n")

	case *ast.HTMLBlock:
		// Escape raw HTML so it doesn't break Telegram's parser.
		r.writeLines(n)
		r.buf.WriteString("\n")

	default:
		// GFM table
		if t, ok := node.(*east.Table); ok {
			r.table(t)
			return
		}
		if node.HasChildren() {
			r.walkBlock(node)
		}
	}
}

// writeLines writes the source lines of a block node (code block, HTML block)
// with HTML escaping.
func (r *renderer) writeLines(n ast.Node) {
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		r.buf.WriteString(escapeHTML(string(seg.Value(r.source))))
	}
}

// ---------------------------------------------------------------------------
// Inline rendering
// ---------------------------------------------------------------------------

func (r *renderer) inlines(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		r.inline(c)
	}
}

func (r *renderer) inline(node ast.Node) {
	switch n := node.(type) {
	case *ast.Text:
		r.buf.WriteString(escapeHTML(string(n.Text(r.source))))
		if n.SoftLineBreak() {
			r.buf.WriteByte('\n')
		}
		if n.HardLineBreak() {
			r.buf.WriteByte('\n')
		}

	case *ast.String:
		r.buf.WriteString(escapeHTML(string(n.Value)))

	case *ast.Emphasis:
		tag := "i"
		if n.Level == 2 {
			tag = "b"
		}
		fmt.Fprintf(&r.buf, "<%s>", tag)
		r.inlines(n)
		fmt.Fprintf(&r.buf, "</%s>", tag)

	case *ast.CodeSpan:
		r.buf.WriteString("<code>")
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch t := c.(type) {
			case *ast.Text:
				r.buf.WriteString(escapeHTML(string(t.Text(r.source))))
			case *ast.String:
				r.buf.WriteString(escapeHTML(string(t.Value)))
			}
		}
		r.buf.WriteString("</code>")

	case *ast.Link:
		fmt.Fprintf(&r.buf, "<a href=\"%s\">", escapeHTML(string(n.Destination)))
		r.inlines(n)
		r.buf.WriteString("</a>")

	case *ast.AutoLink:
		url := string(n.URL(r.source))
		label := string(n.Label(r.source))
		fmt.Fprintf(&r.buf, "<a href=\"%s\">%s</a>", escapeHTML(url), escapeHTML(label))

	case *ast.Image:
		// Telegram doesn't support inline images; render as a link.
		alt := r.textContent(n)
		if alt == "" {
			alt = string(n.Destination)
		}
		fmt.Fprintf(&r.buf, "<a href=\"%s\">%s</a>",
			escapeHTML(string(n.Destination)), escapeHTML(alt))

	case *ast.RawHTML:
		// Escape raw HTML to avoid breaking Telegram's parser.
		for i := 0; i < n.Segments.Len(); i++ {
			seg := n.Segments.At(i)
			r.buf.WriteString(escapeHTML(string(seg.Value(r.source))))
		}

	default:
		// GFM extensions
		switch v := node.(type) {
		case *east.Strikethrough:
			r.buf.WriteString("<s>")
			r.inlines(v)
			r.buf.WriteString("</s>")
		case *east.TaskCheckBox:
			if v.IsChecked {
				r.buf.WriteString("\u2705 ") // ✅
			} else {
				r.buf.WriteString("\u2610 ") // ☐
			}
		default:
			if node.HasChildren() {
				r.inlines(node)
			}
		}
	}
}

// textContent returns the plain-text content of a node tree.
func (r *renderer) textContent(n ast.Node) string {
	var buf bytes.Buffer
	r.collectText(n, &buf)
	return buf.String()
}

func (r *renderer) collectText(node ast.Node, buf *bytes.Buffer) {
	for c := node.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			buf.Write(t.Text(r.source))
		case *ast.String:
			buf.Write(t.Value)
		default:
			r.collectText(c, buf)
		}
	}
}

// ---------------------------------------------------------------------------
// List rendering
// ---------------------------------------------------------------------------

func (r *renderer) list(n *ast.List) {
	idx := 0
	if n.Start > 0 {
		idx = int(n.Start) - 1
	}
	indent := strings.Repeat("  ", r.listDepth)

	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		item, ok := child.(*ast.ListItem)
		if !ok {
			continue
		}
		if n.IsOrdered() {
			idx++
			fmt.Fprintf(&r.buf, "%s%d. ", indent, idx)
		} else {
			r.buf.WriteString(indent)
			r.buf.WriteString("\u2022 ") // •
		}
		r.listItemContent(item)
		r.buf.WriteByte('\n')
	}
	if r.listDepth == 0 {
		r.buf.WriteByte('\n')
	}
}

func (r *renderer) listItemContent(item *ast.ListItem) {
	first := true
	for c := item.FirstChild(); c != nil; c = c.NextSibling() {
		switch n := c.(type) {
		case *ast.Paragraph:
			if !first {
				r.buf.WriteByte('\n')
				r.buf.WriteString(strings.Repeat("  ", r.listDepth+1))
			}
			r.inlines(n)
			first = false
		case *ast.TextBlock:
			if !first {
				r.buf.WriteByte('\n')
				r.buf.WriteString(strings.Repeat("  ", r.listDepth+1))
			}
			r.inlines(n)
			first = false
		case *ast.List:
			r.buf.WriteByte('\n')
			r.listDepth++
			r.list(n)
			r.listDepth--
		default:
			r.block(c)
			first = false
		}
	}
}

// ---------------------------------------------------------------------------
// Table rendering (GFM)
// ---------------------------------------------------------------------------

func (r *renderer) table(t *east.Table) {
	var rows [][]string
	headerIdx := -1

	for child := t.FirstChild(); child != nil; child = child.NextSibling() {
		var cells []string
		isHeader := false

		switch row := child.(type) {
		case *east.TableHeader:
			isHeader = true
			for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
				cells = append(cells, r.textContent(cell))
			}
		case *east.TableRow:
			for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
				cells = append(cells, r.textContent(cell))
			}
		default:
			continue
		}
		if isHeader {
			headerIdx = len(rows)
		}
		rows = append(rows, cells)
	}

	if len(rows) == 0 {
		return
	}

	// Normalise column count.
	numCols := 0
	for _, row := range rows {
		if len(row) > numCols {
			numCols = len(row)
		}
	}
	for i := range rows {
		for len(rows[i]) < numCols {
			rows[i] = append(rows[i], "")
		}
	}

	headers := make([]string, numCols)
	dataRows := rows
	if headerIdx >= 0 && headerIdx < len(rows) {
		copy(headers, rows[headerIdx])
		dataRows = append(rows[:headerIdx], rows[headerIdx+1:]...)
	}
	for i := range headers {
		if strings.TrimSpace(headers[i]) == "" {
			headers[i] = fmt.Sprintf("Column %d", i+1)
		}
	}

	// Fallback for malformed "header-only" tables: keep one shell row.
	if len(dataRows) == 0 {
		dataRows = [][]string{make([]string, numCols)}
	}

	for i, row := range dataRows {
		fmt.Fprintf(&r.buf, "<b>%d.</b>\n", i+1)
		for j, cell := range row {
			r.buf.WriteString("• <b>")
			r.buf.WriteString(escapeHTML(headers[j]))
			r.buf.WriteString("</b>: ")
			r.buf.WriteString(escapeHTML(cell))
			r.buf.WriteByte('\n')
		}
		if i < len(dataRows)-1 {
			r.buf.WriteByte('\n')
		}
	}
	r.buf.WriteByte('\n')
}
