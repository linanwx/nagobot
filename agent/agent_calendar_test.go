package agent

import (
	"strings"
	"testing"
	"time"
)

func TestRenderPromptReplacesCalendarPlaceholder(t *testing.T) {
	loc := time.FixedZone("UTC+08", 8*60*60)
	now := time.Date(2026, 2, 8, 15, 4, 0, 0, loc)

	got := renderPrompt("Time: {{TIME}}\nCalendar:\n{{CALENDAR}}", PromptContext{Time: now}, nil)

	if strings.Contains(got, "{{CALENDAR}}") {
		t.Fatalf("calendar placeholder should be replaced, got: %q", got)
	}
	if !strings.Contains(got, "Timezone: UTC+08 (UTC+08:00)") {
		t.Fatalf("calendar should include timezone and offset, got: %q", got)
	}
	if !strings.Contains(got, "-1d: 2026-02-07 (Yesterday, Saturday)") {
		t.Fatalf("calendar should include Yesterday marker, got: %q", got)
	}
	if !strings.Contains(got, "+0d: 2026-02-08 (Today, Sunday)") {
		t.Fatalf("calendar should include Today marker, got: %q", got)
	}
	if !strings.Contains(got, "+1d: 2026-02-09 (Tomorrow, Monday)") {
		t.Fatalf("calendar should include Tomorrow marker, got: %q", got)
	}
}

func TestFormatCalendarCrossYearWindow(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	got := formatCalendar(now)
	lines := strings.Split(got, "\n")
	if len(lines) != 16 {
		t.Fatalf("calendar should contain 16 lines (timezone + 15 days), got %d", len(lines))
	}
	if !strings.Contains(got, "Timezone: UTC (UTC+00:00)") {
		t.Fatalf("calendar should include UTC timezone line, got: %q", got)
	}
	if !strings.Contains(got, "-7d: 2025-12-25 (Thursday)") {
		t.Fatalf("calendar should include lower boundary date, got: %q", got)
	}
	if !strings.Contains(got, "-1d: 2025-12-31 (Yesterday, Wednesday)") {
		t.Fatalf("calendar should include cross-year yesterday date, got: %q", got)
	}
	if !strings.Contains(got, "+0d: 2026-01-01 (Today, Thursday)") {
		t.Fatalf("calendar should include current date, got: %q", got)
	}
	if !strings.Contains(got, "+1d: 2026-01-02 (Tomorrow, Friday)") {
		t.Fatalf("calendar should include next date, got: %q", got)
	}
	if !strings.Contains(got, "+7d: 2026-01-08 (Thursday)") {
		t.Fatalf("calendar should include upper boundary date, got: %q", got)
	}
}

func TestNewRawAgentRendersCalendar(t *testing.T) {
	now := time.Date(2026, 2, 8, 10, 0, 0, 0, time.UTC)
	ag := NewRawAgent("raw", "Calendar:\n{{CALENDAR}}")

	got := ag.BuildPrompt(PromptContext{Time: now})
	if !strings.Contains(got, "+0d: 2026-02-08 (Today, Sunday)") {
		t.Fatalf("raw agent prompt should include rendered calendar, got: %q", got)
	}
}
