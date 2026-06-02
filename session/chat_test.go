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
		strings.Repeat("A", 250) + strings.Repeat("Z", 250), // 500 runes → head 200 + " [...] " + tail 200
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
	// Head+tail preview: every entry is capped at head + tail + the marker.
	maxContent := chatPreviewHead + chatPreviewTail + runeLen(" [...] ")
	for _, line := range lines {
		role, content, ok := strings.Cut(line, ": ")
		if !ok {
			t.Errorf("malformed line: %q", line)
			continue
		}
		if role != ChatRoleUser && role != ChatRoleAssistant {
			t.Errorf("unexpected role %q in line %q", role, line)
		}
		if runeLen(content) > maxContent {
			t.Errorf("content exceeds %d runes (%d): %q", maxContent, runeLen(content), content)
		}
	}
	// The 500-rune assistant entry must keep BOTH its head and tail with the
	// middle elided — head-only truncation (the prior behavior) would have
	// dropped the trailing Z run entirely.
	long := lines[3]
	if !strings.HasPrefix(long, "assistant: "+strings.Repeat("A", chatPreviewHead)+" [...] ") {
		t.Errorf("elided entry should start with head + marker, got %q", long)
	}
	if !strings.HasSuffix(long, " [...] "+strings.Repeat("Z", chatPreviewTail)) {
		t.Errorf("elided entry should end with marker + tail, got %q", long)
	}
	if strings.Contains(long, strings.Repeat("A", chatPreviewHead+1)) {
		t.Errorf("middle should be elided, found more than %d head runes: %q", chatPreviewHead, long)
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
