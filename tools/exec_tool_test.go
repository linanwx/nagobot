package tools

import (
	"testing"
)

func TestRmPatternMatches(t *testing.T) {
	shouldMatch := []string{
		"rm foo.txt",
		"rm -rf ./build",
		"rm -f a.txt b.txt",
		"rm --recursive --force dir/",
		"echo hello; rm -rf dist",
		"echo ok | rm foo",
		"cmd && rm bar",
	}
	for _, cmd := range shouldMatch {
		if !rmPattern.MatchString(cmd) {
			t.Errorf("rmPattern should match %q", cmd)
		}
	}
}

func TestRmPatternFalsePositives(t *testing.T) {
	shouldNotMatch := []string{
		"cargo build",
		"gorm migrate",
		"xrm something",
		"echo remove",
		"perform action",
		"git remote add origin",
		"ls -la",
	}
	for _, cmd := range shouldNotMatch {
		if rmPattern.MatchString(cmd) {
			t.Errorf("rmPattern should NOT match %q", cmd)
		}
	}
}
