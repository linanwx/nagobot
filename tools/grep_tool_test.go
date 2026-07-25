package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The fallback path must speak the same regex dialect as ripgrep. Bare grep is
// BRE, where `|` is a literal character, so an alternation pattern matched
// nothing on a host without rg — silently, and indistinguishably from "the
// text is not there".
func TestGrepFallbackUsesExtendedRegex(t *testing.T) {
	tool := &GrepTool{workspace: t.TempDir()}
	args := tool.buildGrepArgs(grepArgs{Pattern: "a|b"}, tool.workspace)
	if len(args) == 0 || !strings.Contains(args[0], "E") {
		t.Fatalf("grep fallback must pass -E for ERE, got %v", args)
	}
}

// End-to-end through whichever backend this host actually has: an alternation
// pattern must find both alternatives.
func TestGrepAlternationMatchesBothBranches(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		if _, err := exec.LookPath("grep"); err != nil {
			t.Skip("neither rg nor grep available")
		}
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "2026-03-14.md")
	body := "line one mentions 护照 renewal\nline two mentions passport office\nline three mentions neither\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &GrepTool{workspace: dir}
	raw, _ := json.Marshal(grepArgs{
		Pattern: "护照|passport",
		Include: "20??-??-??.md",
	})

	out := tool.run(context.Background(), raw)
	if !strings.Contains(out, "护照") || !strings.Contains(out, "passport") {
		t.Fatalf("alternation lost a branch; output:\n%s", out)
	}
	if strings.Contains(out, "neither") {
		t.Fatalf("matched a non-matching line; output:\n%s", out)
	}
}
