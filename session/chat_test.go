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

	got := ReadRecentChat(dir, 5, time.Local)
	lines := strings.Split(got, "\n")
	// 7 entries seeded, 5 requested → earlier ones dropped, so the first line is
	// the collapse marker followed by the 5 kept entries.
	if len(lines) != 6 {
		t.Fatalf("want 6 lines (collapse marker + 5 entries), got %d:\n%s", len(lines), got)
	}
	if !strings.Contains(lines[0], "collapsed") {
		t.Errorf("first line should be the collapse marker, got %q", lines[0])
	}
	// Every entry line carries a relative-time prefix. Seeds use time.Now(), so
	// they all render "[Today HH:MM] ". Strip it to assert on the role/content.
	entries := make([]string, 0, 5)
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, "[Today ") {
			t.Errorf("entry line missing [Today HH:MM] prefix, got %q", line)
		}
		_, rest, ok := strings.Cut(line, "] ")
		if !ok {
			t.Fatalf("entry line has no time-prefix terminator: %q", line)
		}
		entries = append(entries, rest)
	}
	// Chronological: oldest of the last 5 first.
	if !strings.HasPrefix(entries[0], "user: third") {
		t.Errorf("first kept line should be `user: third`, got %q", entries[0])
	}
	// Newline-collapse: the truncated entry must use spaces.
	if strings.Contains(got, "\nwith newline") {
		t.Errorf("expected newlines collapsed, found embedded newline: %q", got)
	}
	// Head+tail preview: every entry is capped at head + tail + the marker.
	maxContent := chatPreviewHead + chatPreviewTail + runeLen(" [...] ")
	for _, line := range entries {
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
	long := entries[3]
	if !strings.HasPrefix(long, "assistant: "+strings.Repeat("A", chatPreviewHead)+" [...] ") {
		t.Errorf("elided entry should start with head + marker, got %q", long)
	}
	if !strings.HasSuffix(long, " [...] "+strings.Repeat("Z", chatPreviewTail)) {
		t.Errorf("elided entry should end with marker + tail, got %q", long)
	}
	if strings.Contains(long, strings.Repeat("A", chatPreviewHead+1)) {
		t.Errorf("middle should be elided, found more than %d head runes: %q", chatPreviewHead, long)
	}
	// Last entry should be the 7th seeded entry, with collapsed spaces.
	if !strings.HasPrefix(entries[4], "user: seventh with extra spaces") {
		t.Errorf("last line should be `user: seventh with extra spaces`, got %q", entries[4])
	}
}

// TestReadRecentChat_NoMarkerWhenNotTruncated: when the whole conversation fits
// within n, no collapse marker is added.
func TestReadRecentChat_NoMarkerWhenNotTruncated(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i, body := range []string{"a", "b", "c"} {
		if err := AppendChat(dir, ChatRoleUser, body, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	got := ReadRecentChat(dir, 5, time.Local) // 3 entries, 5 requested → nothing dropped
	if strings.Contains(got, "collapsed") {
		t.Errorf("no marker expected when not truncated, got:\n%s", got)
	}
	if lines := strings.Split(got, "\n"); len(lines) != 3 {
		t.Errorf("want 3 lines, got %d:\n%s", len(lines), got)
	}
}

func TestReadRecentChat_MissingFile(t *testing.T) {
	if got := ReadRecentChat(t.TempDir(), 5, time.Local); got != "" {
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

func TestFormatChatTime(t *testing.T) {
	loc := time.UTC
	now := time.Date(2029, 12, 22, 9, 0, 0, 0, loc)

	cases := []struct {
		name string
		ts   time.Time
		want string
	}{
		{"today", time.Date(2029, 12, 22, 23, 29, 0, 0, loc), "Today 23:29"},
		{"today early", time.Date(2029, 12, 22, 0, 5, 0, 0, loc), "Today 00:05"},
		{"yesterday", time.Date(2029, 12, 21, 11, 30, 0, 0, loc), "Yesterday 11:30"},
		{"yesterday late", time.Date(2029, 12, 21, 23, 59, 0, 0, loc), "Yesterday 23:59"},
		{"older", time.Date(2029, 12, 18, 18, 32, 0, 0, loc), "2029-12-18 18:32"},
		{"future", time.Date(2029, 12, 25, 8, 0, 0, 0, loc), "2029-12-25 08:00"},
		{"zero ts", time.Time{}, ""},
	}
	for _, c := range cases {
		if got := formatChatTime(c.ts, now, loc); got != c.want {
			t.Errorf("%s: formatChatTime = %q, want %q", c.name, got, c.want)
		}
	}

	// Cross-timezone: a UTC instant that is "yesterday 23:30 UTC" but "today"
	// in Asia/Shanghai (UTC+8) must read as Today in the Shanghai location.
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		nowSH := time.Date(2029, 12, 22, 10, 0, 0, 0, sh)
		tsUTC := time.Date(2029, 12, 22, 1, 0, 0, 0, time.UTC) // = 09:00 SH, same day
		if got := formatChatTime(tsUTC, nowSH, sh); got != "Today 09:00" {
			t.Errorf("cross-tz: got %q, want %q", got, "Today 09:00")
		}
	}
}
