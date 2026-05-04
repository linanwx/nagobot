package channel

import (
	"reflect"
	"testing"
)

func TestParseMarkdownDocs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []parsedDoc
	}{
		{
			name: "single doc link",
			in:   "see [report](report.pdf) please",
			want: []parsedDoc{{Label: "report", RawPath: "report.pdf"}},
		},
		{
			name: "absolute path",
			in:   "[q1](/Users/me/q1.pdf)",
			want: []parsedDoc{{Label: "q1", RawPath: "/Users/me/q1.pdf"}},
		},
		{
			name: "image syntax is skipped",
			in:   "![pic](pic.png)",
			want: nil,
		},
		{
			name: "image and doc on same line",
			in:   "![pic](pic.png) and [doc](doc.pdf)",
			want: []parsedDoc{{Label: "doc", RawPath: "doc.pdf"}},
		},
		{
			name: "URL is rejected",
			in:   "[Discord](https://discord.com)",
			want: nil,
		},
		{
			name: "anchor is rejected",
			in:   "[heading](#section)",
			want: nil,
		},
		{
			name: "mailto is rejected",
			in:   "[email](mailto:a@b.com)",
			want: nil,
		},
		{
			name: "empty label is allowed",
			in:   "[](file.pdf)",
			want: []parsedDoc{{Label: "", RawPath: "file.pdf"}},
		},
		{
			name: "inline code is skipped",
			in:   "use `[label](path)` syntax",
			want: nil,
		},
		{
			name: "fenced code block is skipped",
			in:   "```\n[doc](report.pdf)\n```",
			want: nil,
		},
		{
			name: "tilde fence is skipped",
			in:   "~~~\n[doc](report.pdf)\n~~~",
			want: nil,
		},
		{
			name: "multiple docs",
			in:   "[a](a.pdf) [b](b.docx)",
			want: []parsedDoc{
				{Label: "a", RawPath: "a.pdf"},
				{Label: "b", RawPath: "b.docx"},
			},
		},
		{
			name: "no brackets at all",
			in:   "plain text without links",
			want: nil,
		},
		{
			name: "URL link mixed with local doc",
			in:   "[home](https://x.com) and [paper](paper.pdf)",
			want: []parsedDoc{{Label: "paper", RawPath: "paper.pdf"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMarkdownDocs(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseMarkdownDocs(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}
