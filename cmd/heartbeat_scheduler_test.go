package cmd

import (
	"testing"
	"time"
)

func TestColdStartAlignment(t *testing.T) {
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		nowOffset time.Duration
		wantFire  bool // should now.Sub(lp) >= interval?
		wantIntv  time.Duration
	}{
		{
			name:      "not quiet enough (5m)",
			nowOffset: 5 * time.Minute,
			wantFire:  false,
			wantIntv:  hbQuietMin,
		},
		{
			name:      "exactly at first pulse (10m)",
			nowOffset: hbQuietMin,
			wantFire:  true,
			wantIntv:  hbPulseInterval,
		},
		{
			name:      "just past first pulse (11m) - wait for +40m",
			nowOffset: 11 * time.Minute,
			wantFire:  false,
			wantIntv:  hbPulseInterval,
		},
		{
			name:      "between first and second pulse (25m) - wait for +40m",
			nowOffset: 25 * time.Minute,
			wantFire:  false,
			wantIntv:  hbPulseInterval,
		},
		{
			name:      "exactly at second pulse (40m)",
			nowOffset: 40 * time.Minute,
			wantFire:  true,
			wantIntv:  hbPulseInterval,
		},
		{
			name:      "just past second pulse (41m) - wait for +70m",
			nowOffset: 41 * time.Minute,
			wantFire:  false,
			wantIntv:  hbPulseInterval,
		},
		{
			name:      "exactly at third pulse (70m)",
			nowOffset: 70 * time.Minute,
			wantFire:  true,
			wantIntv:  hbPulseInterval,
		},
		{
			name:      "long idle 7 days - wait for next aligned point",
			nowOffset: 7 * 24 * time.Hour,
			wantFire:  false,
			wantIntv:  hbPulseInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := base.Add(tt.nowOffset)
			lp, interval := coldStartAlignment(base, now)

			if interval != tt.wantIntv {
				t.Errorf("interval = %v, want %v", interval, tt.wantIntv)
			}

			fired := now.Sub(lp) >= interval
			if fired != tt.wantFire {
				t.Errorf("fire = %v, want %v (lp=%v, now-lp=%v, interval=%v)",
					fired, tt.wantFire, lp.Format(time.RFC3339), now.Sub(lp), interval)
			}
		})
	}
}
