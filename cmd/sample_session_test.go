package cmd

import "testing"

func TestBodyFromFrontmatter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no frontmatter",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "single frontmatter",
			input: "---\nsource: telegram\nsender: user\n---\nhello",
			want:  "hello",
		},
		{
			name:  "nested frontmatter no gap",
			input: "---\nsource: child_completed\nsender: system\n---\n---\ntype: child_completed\nsender: system\nagent: search\n---\nActual result content",
			want:  "Actual result content",
		},
		{
			name:  "nested frontmatter with blank line (real wake payload)",
			input: "---\nsource: child_completed\nsender: system\n---\n\n---\ntype: child_completed\nsender: system\nagent: search\n---\nActual result content",
			want:  "Actual result content",
		},
		{
			name:  "triple nested with gaps",
			input: "---\na: 1\n---\n\n---\nb: 2\n---\n\n---\nc: 3\n---\ndeep body",
			want:  "deep body",
		},
		{
			name:  "frontmatter only no body",
			input: "---\na: 1\n---\n",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bodyFromFrontmatter(tt.input)
			if got != tt.want {
				t.Errorf("bodyFromFrontmatter() = %q, want %q", got, tt.want)
			}
		})
	}
}
