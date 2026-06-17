package monitor

import (
	"testing"
	"time"
)

func TestWindowDuration(t *testing.T) {
	ok := map[Window]time.Duration{
		"1h":     time.Hour,
		"90m":    90 * time.Minute,
		"12h":    12 * time.Hour,
		"1d":     24 * time.Hour,
		"7d":     7 * 24 * time.Hour,
		"30d":    30 * 24 * time.Hour,
		"2w":     2 * 7 * 24 * time.Hour,
		"1h30m":  90 * time.Minute,
		"1.5d":   36 * time.Hour,
	}
	for w, want := range ok {
		got, err := w.Duration()
		if err != nil {
			t.Errorf("%q: unexpected error %v", w, err)
			continue
		}
		if got != want {
			t.Errorf("%q: got %v, want %v", w, got, want)
		}
	}

	bad := []Window{"", "30", "d", "abc", "0d", "-5d", "7x", "30days"}
	for _, w := range bad {
		if _, err := w.Duration(); err == nil {
			t.Errorf("%q: expected error, got nil", w)
		}
	}
}
