package thread

import (
	"strings"
	"testing"
)

func TestResultPreview_SkipsFrontmatter(t *testing.T) {
	// Typical tool result: YAML frontmatter header + body.
	withFM := "---\ntool: web_fetch\nstatus: ok\nsource: kimi-global\n---\n\nIBM unveils new 1000-qubit chip; roadmap targets 2030 fault tolerance."
	got := resultPreview(withFM)
	if strings.Contains(got, "tool: web_fetch") || strings.Contains(got, "status: ok") {
		t.Errorf("frontmatter not skipped: %q", got)
	}
	if !strings.HasPrefix(got, "IBM unveils new 1000-qubit chip") {
		t.Errorf("body not previewed: %q", got)
	}

	// No frontmatter → whole result is previewed.
	plain := "8 results found for quantum computing"
	if got := resultPreview(plain); got != plain {
		t.Errorf("plain result altered: %q", got)
	}

	// Truncates to 200 chars.
	long := "---\ntool: x\n---\n\n" + strings.Repeat("a", 500)
	if got := resultPreview(long); len(got) > 203 { // 200 + "..."
		t.Errorf("not truncated to 200: %d chars", len(got))
	}
}
