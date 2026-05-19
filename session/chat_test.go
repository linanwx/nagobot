package session

import (
	"strings"
	"testing"
	"time"
)

func TestReadRecentChat(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// Seed 7 entries; ReadRecentChat(5) should return the last 5 in chronological order.
	for i, body := range []string{
		"first",
		"second\nwith newline",
		"third",
		"fourth",
		"fifth",
		strings.Repeat("漢", 250), // 250 runes, must be truncated to 200
		"seventh   with    extra spaces",
	} {
		role := ChatRoleUser
		if i%2 == 1 {
			role = ChatRoleAssistant
		}
		if err := AppendChat(dir, role, body, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	got := ReadRecentChat(dir, 5)
	lines := strings.Split(got, "\n")
	if len(lines) != 5 {
		t.Fatalf("want 5 lines, got %d:\n%s", len(lines), got)
	}
	// Chronological: oldest of the last 5 first.
	if !strings.HasPrefix(lines[0], "user: third") {
		t.Errorf("first kept line should be `user: third`, got %q", lines[0])
	}
	// Newline-collapse: the truncated entry must use spaces.
	if strings.Contains(got, "\nwith newline") {
		t.Errorf("expected newlines collapsed, found embedded newline: %q", got)
	}
	// Rune-truncation: the 250-rune line should now have at most 200 runes
	// after the "user: " / "assistant: " prefix.
	for _, line := range lines {
		role, content, ok := strings.Cut(line, ": ")
		if !ok {
			t.Errorf("malformed line: %q", line)
			continue
		}
		if role != ChatRoleUser && role != ChatRoleAssistant {
			t.Errorf("unexpected role %q in line %q", role, line)
		}
		if runeLen(content) > 200 {
			t.Errorf("content exceeds 200 runes (%d): %q", runeLen(content), content)
		}
	}
	// Last line should be the 7th seeded entry, with collapsed spaces.
	if !strings.HasPrefix(lines[4], "user: seventh with extra spaces") {
		t.Errorf("last line should be `user: seventh with extra spaces`, got %q", lines[4])
	}
}

func TestReadRecentChat_MissingFile(t *testing.T) {
	if got := ReadRecentChat(t.TempDir(), 5); got != "" {
		t.Errorf("expected empty string for missing file, got %q", got)
	}
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
