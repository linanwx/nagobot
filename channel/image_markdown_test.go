package channel

import (
	"reflect"
	"testing"
)

func TestParseMarkdownImages(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []parsedImage
	}{
		{
			name: "single image on its own line",
			in:   "Here is a picture:\n![cat](/tmp/cat.jpg)\nDone.",
			want: []parsedImage{{Alt: "cat", RawPath: "/tmp/cat.jpg"}},
		},
		{
			name: "image inline mid-sentence",
			in:   "before ![dog](dog.png) after",
			want: []parsedImage{{Alt: "dog", RawPath: "dog.png"}},
		},
		{
			name: "two images on one line",
			in:   "![a](/a.jpg) and ![b](/b.jpg)",
			want: []parsedImage{{Alt: "a", RawPath: "/a.jpg"}, {Alt: "b", RawPath: "/b.jpg"}},
		},
		{
			name: "two images on separate lines",
			in:   "![a](/a.jpg)\n![b](/b.jpg)",
			want: []parsedImage{{Alt: "a", RawPath: "/a.jpg"}, {Alt: "b", RawPath: "/b.jpg"}},
		},
		{
			name: "image inside fenced code block is skipped",
			in:   "before\n```\n![nope](/nope.jpg)\n```\nafter",
			want: nil,
		},
		{
			name: "image after closed fenced block is extracted",
			in:   "```\ncode\n```\n![ok](/ok.jpg)",
			want: []parsedImage{{Alt: "ok", RawPath: "/ok.jpg"}},
		},
		{
			name: "image inside inline code is skipped",
			in:   "use `![nope](x.jpg)` literally",
			want: nil,
		},
		{
			name: "inline code closed before image",
			in:   "`code` then ![ok](/ok.jpg)",
			want: []parsedImage{{Alt: "ok", RawPath: "/ok.jpg"}},
		},
		{
			name: "empty alt",
			in:   "![](/foo.jpg)",
			want: []parsedImage{{Alt: "", RawPath: "/foo.jpg"}},
		},
		{
			name: "image with title (title ignored)",
			in:   `![alt](/foo.jpg "Title")`,
			want: []parsedImage{{Alt: "alt", RawPath: "/foo.jpg"}},
		},
		{
			name: "malformed missing close paren",
			in:   "![alt](/foo.jpg",
			want: nil,
		},
		{
			name: "non-image link is not matched",
			in:   "[not image](/foo.jpg)",
			want: nil,
		},
		{
			name: "relative path preserved as-is",
			in:   "![pic](media/pic.jpg)",
			want: []parsedImage{{Alt: "pic", RawPath: "media/pic.jpg"}},
		},
		{
			name: "tilde fences also count",
			in:   "~~~\n![nope](x.jpg)\n~~~",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMarkdownImages(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseMarkdownImages(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseMarkdownImages_StreamingChunkSafety(t *testing.T) {
	// Each chunk in the streaming pipeline is a complete Markdown block.
	// Parsing each chunk independently must not double-extract images
	// that span chunk boundaries (they don't, by construction — verify here).
	chunks := []string{
		"Hello ![pic](/a.jpg).\n\n",
		"More text\n",
		"```\nlater ![inside](/b.jpg)\n```\n",
		"final ![end](/c.jpg)",
	}
	var all []parsedImage
	for _, c := range chunks {
		all = append(all, parseMarkdownImages(c)...)
	}
	want := []parsedImage{
		{Alt: "pic", RawPath: "/a.jpg"},
		{Alt: "end", RawPath: "/c.jpg"},
	}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("got %#v, want %#v", all, want)
	}
}
