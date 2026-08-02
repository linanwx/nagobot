package channel

import (
	"errors"
	"testing"
	"time"

	"github.com/linanwx/nagobot/config"
)

// The built-in maintenance jobs digest what humans did. On a deployment nobody
// is talking to, they used to run anyway — 2, 3 and 9 days of silence across
// three of four live deployments on 2026-08-02, with all three jobs still
// firing nightly.
func TestShouldSkipJob(t *testing.T) {
	const builtin = "world-knowledge"

	tests := []struct {
		name string
		id   string
		fn   func() (time.Time, error)
		want bool
	}{
		{
			name: "built-in job, human spoke an hour ago",
			id:   builtin,
			fn:   func() (time.Time, error) { return time.Now().Add(-time.Hour), nil },
			want: false,
		},
		{
			name: "built-in job, human spoke 23h ago — still inside the window",
			id:   builtin,
			fn:   func() (time.Time, error) { return time.Now().Add(-23 * time.Hour), nil },
			want: false,
		},
		{
			name: "built-in job, human spoke 25h ago",
			id:   builtin,
			fn:   func() (time.Time, error) { return time.Now().Add(-25 * time.Hour), nil },
			want: true,
		},
		{
			name: "built-in job, nine days of silence (mengbei on 2026-08-02)",
			id:   builtin,
			fn:   func() (time.Time, error) { return time.Now().Add(-9 * 24 * time.Hour), nil },
			want: true,
		},
		{
			// A successful scan that found no human at all is idle by
			// definition — not an error, and not a reason to run.
			name: "built-in job, scan found no human ever",
			id:   builtin,
			fn:   func() (time.Time, error) { return time.Time{}, nil },
			want: true,
		},
		{
			// Fail open: an unreadable session tree must not quietly disable
			// maintenance for good.
			name: "built-in job, scan failed",
			id:   builtin,
			fn:   func() (time.Time, error) { return time.Time{}, errors.New("sessions unreadable") },
			want: false,
		},
		{
			// Same, for a deployment where nothing injected the clock.
			name: "built-in job, no clock injected",
			id:   builtin,
			fn:   nil,
			want: false,
		},
		{
			// Custom jobs are the user's own schedule. Several on the live
			// deployments push weekly reports to people who are deliberately not
			// daily chat users; gating those would stop the reports for exactly
			// their intended audience.
			name: "custom job is never gated, however quiet the deployment",
			id:   "weekly-bidding-wednesday",
			fn:   func() (time.Time, error) { return time.Now().Add(-90 * 24 * time.Hour), nil },
			want: false,
		},
		{
			name: "unnamed job is not a built-in",
			id:   "",
			fn:   func() (time.Time, error) { return time.Time{}, nil },
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &CronChannel{lastUserActive: tc.fn}
			if got := c.shouldSkipJob(tc.id); got != tc.want {
				t.Errorf("shouldSkipJob(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

// Every shipped seed must be gated, not just the three that exist today. A
// fourth seed added to defaultCronSeeds and forgotten here would otherwise run
// on idle deployments forever, silently — which is the failure this whole
// change exists to remove.
func TestEveryBuiltinSeedIsGated(t *testing.T) {
	c := &CronChannel{lastUserActive: func() (time.Time, error) {
		return time.Now().Add(-30 * 24 * time.Hour), nil
	}}
	cfg := config.DefaultConfig()
	if len(cfg.Cron) == 0 {
		t.Fatal("DefaultConfig carries no cron seeds; the gate would be untested")
	}
	for _, seed := range cfg.Cron {
		if !config.IsBuiltinCronJob(seed.ID) {
			t.Errorf("seed %q is not recognized as built-in", seed.ID)
		}
		if !c.shouldSkipJob(seed.ID) {
			t.Errorf("seed %q still fires after 30 days of silence", seed.ID)
		}
	}
}
