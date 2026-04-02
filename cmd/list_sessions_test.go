package cmd

import (
	"testing"
	"time"
)

func TestNeedSummaryFilter(t *testing.T) {
	now := time.Date(2026, 4, 1, 22, 0, 0, 0, time.UTC)

	fmtTime := func(t time.Time) string { return t.Format(time.RFC3339) }

	sessions := []sessionEntry{
		// Should EXCLUDE: updated <1h ago
		{Key: "telegram:123", UpdatedAt: fmtTime(now.Add(-30 * time.Minute)), ChangedSinceSummary: true, MessageCount: 100},
		// Should EXCLUDE: cron + summary <2d
		{Key: "cron:daily-check", UpdatedAt: fmtTime(now.Add(-3 * time.Hour)), SummaryAt: fmtTime(now.Add(-20 * time.Hour)), ChangedSinceSummary: true, MessageCount: 50},
		// Should INCLUDE: cron + summary >2d
		{Key: "cron:old-task", UpdatedAt: fmtTime(now.Add(-3 * time.Hour)), SummaryAt: fmtTime(now.Add(-72 * time.Hour)), ChangedSinceSummary: true, MessageCount: 80},
		// Should EXCLUDE: total_messages >500 + summary <2d
		{Key: "discord:big", UpdatedAt: fmtTime(now.Add(-2 * time.Hour)), SummaryAt: fmtTime(now.Add(-36 * time.Hour)), TotalMessages: 700, ChangedSinceSummary: true, MessageCount: 200},
		// Should INCLUDE: total_messages >500 but summary >2d
		{Key: "discord:big-stale", UpdatedAt: fmtTime(now.Add(-2 * time.Hour)), SummaryAt: fmtTime(now.Add(-72 * time.Hour)), TotalMessages: 700, ChangedSinceSummary: true, MessageCount: 300},
		// Should EXCLUDE: thread + updated >12h
		{Key: "telegram:123:threads:2026-03-31-10-00-00-abc", UpdatedAt: fmtTime(now.Add(-14 * time.Hour)), ChangedSinceSummary: true, MessageCount: 40},
		// Should INCLUDE: thread + updated <12h
		{Key: "telegram:123:threads:2026-04-01-18-00-00-def", UpdatedAt: fmtTime(now.Add(-4 * time.Hour)), ChangedSinceSummary: true, MessageCount: 30},
		// Should INCLUDE: normal session, not recently summarized
		{Key: "discord:normal", UpdatedAt: fmtTime(now.Add(-5 * time.Hour)), SummaryAt: fmtTime(now.Add(-30 * time.Hour)), TotalMessages: 100, ChangedSinceSummary: true, MessageCount: 100},
	}

	output := &listSessionsOutput{Sessions: sessions}
	applyNeedSummaryFilter(output, now)

	wantKeys := []string{"cron:old-task", "discord:big-stale", "telegram:123:threads:2026-04-01-18-00-00-def", "discord:normal"}
	if len(output.Sessions) != len(wantKeys) {
		var gotKeys []string
		for _, s := range output.Sessions {
			gotKeys = append(gotKeys, s.Key)
		}
		t.Fatalf("expected %d sessions %v, got %d: %v", len(wantKeys), wantKeys, len(output.Sessions), gotKeys)
	}
	for i, want := range wantKeys {
		if output.Sessions[i].Key != want {
			t.Errorf("session[%d] = %q, want %q", i, output.Sessions[i].Key, want)
		}
	}
}
