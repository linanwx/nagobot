package tools

import (
	"strings"
	"testing"
)

func TestRewriteRmToTrash(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantRm  bool   // expect rewrite
		wantMv  string // substring that must appear in rewritten command (empty = skip check)
		wantNo  string // substring that must NOT appear in rewritten command
	}{
		{
			name:    "simple rm",
			command: "rm foo.txt",
			wantRm:  true,
			wantMv:  "mv foo.txt",
		},
		{
			name:    "rm -rf",
			command: "rm -rf ./build",
			wantRm:  true,
			wantMv:  "mv ./build",
			wantNo:  "-rf",
		},
		{
			name:    "rm -f with multiple files",
			command: "rm -f a.txt b.txt",
			wantRm:  true,
			wantMv:  "mv a.txt b.txt",
		},
		{
			name:    "rm --recursive --force",
			command: "rm --recursive --force dir/",
			wantRm:  true,
			wantMv:  "mv dir/",
			wantNo:  "--recursive",
		},
		{
			name:    "no rm - ls command",
			command: "ls -la",
			wantRm:  false,
		},
		{
			name:    "no rm - cargo build",
			command: "cargo build",
			wantRm:  false,
		},
		{
			name:    "no rm - gorm",
			command: "gorm migrate",
			wantRm:  false,
		},
		{
			name:    "no rm - echo rm",
			command: "echo remove this",
			wantRm:  false,
		},
		{
			name:    "chained command with rm",
			command: "echo hello; rm -rf dist",
			wantRm:  true,
			wantMv:  "mv dist",
		},
		{
			name:    "pipe before rm",
			command: "echo ok | rm foo",
			wantRm:  true,
			wantMv:  "mv foo",
		},
		{
			name:    "mkdir prepended",
			command: "rm foo",
			wantRm:  true,
			wantMv:  "mkdir -p",
		},
		{
			name:    "trash dir in path",
			command: "rm foo",
			wantRm:  true,
			wantMv:  ".nagobot-trash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rewritten, trashDir := rewriteRmToTrash(tt.command)
			if tt.wantRm {
				if rewritten == "" {
					t.Fatal("expected rewrite, got empty")
				}
				if trashDir == "" {
					t.Fatal("expected trashDir, got empty")
				}
				if !strings.Contains(trashDir, trashDirName) {
					t.Errorf("trashDir %q should contain %q", trashDir, trashDirName)
				}
				if tt.wantMv != "" && !strings.Contains(rewritten, tt.wantMv) {
					t.Errorf("rewritten %q should contain %q", rewritten, tt.wantMv)
				}
				if tt.wantNo != "" && strings.Contains(rewritten, tt.wantNo) {
					t.Errorf("rewritten %q should NOT contain %q", rewritten, tt.wantNo)
				}
				// Must not contain "rm " anymore
				if rmPattern.MatchString(rewritten) {
					t.Errorf("rewritten %q still contains rm command", rewritten)
				}
			} else {
				if rewritten != "" {
					t.Errorf("expected no rewrite, got %q", rewritten)
				}
			}
		})
	}
}

func TestStripRmFlags(t *testing.T) {
	tests := []struct {
		in, wantContains, wantNotContain string
	}{
		{"mv -rf foo", "mv", "-rf"},
		{"mv --recursive --force foo", "mv", "--recursive"},
		{"mv -i foo", "foo", "-i"},
		{"mv -v --verbose foo", "foo", "--verbose"},
		{"mv --no-preserve-root foo", "foo", "--no-preserve-root"},
	}
	for _, tt := range tests {
		got := stripRmFlags(tt.in)
		if !strings.Contains(got, tt.wantContains) {
			t.Errorf("stripRmFlags(%q) = %q, want to contain %q", tt.in, got, tt.wantContains)
		}
		if strings.Contains(got, tt.wantNotContain) {
			t.Errorf("stripRmFlags(%q) = %q, should not contain %q", tt.in, got, tt.wantNotContain)
		}
	}
}

func TestRmPatternFalsePositives(t *testing.T) {
	// These should NOT match.
	commands := []string{
		"cargo build",
		"gorm migrate",
		"xrm something",
		"echo remove",
		"perform action",
		"git remote add origin",
	}
	for _, cmd := range commands {
		if rmPattern.MatchString(cmd) {
			t.Errorf("rmPattern should NOT match %q", cmd)
		}
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"it's", "'it'\"'\"'s'"},
	}
	for _, tt := range tests {
		got := shellQuote(tt.in)
		if got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
