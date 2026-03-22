package cmd

import (
	"testing"
	"time"
)

func TestLatestDueTrigger(t *testing.T) {
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		interval  time.Duration
		nowOffset time.Duration
		wantZero  bool          // expect zero (no trigger due)
		wantPoint time.Duration // expected trigger point offset from base
	}{
		{
			name:      "not quiet enough (5m)",
			interval:  hbPulseInterval,
			nowOffset: 5 * time.Minute,
			wantZero:  true,
		},
		{
			name:      "exactly at first pulse (10m)",
			interval:  hbPulseInterval,
			nowOffset: hbQuietMin,
			wantPoint: 10 * time.Minute,
		},
		{
			name:      "just past first pulse (11m) — trigger is at +10m",
			interval:  hbPulseInterval,
			nowOffset: 11 * time.Minute,
			wantPoint: 10 * time.Minute,
		},
		{
			name:      "between first and second (25m) — trigger is at +10m",
			interval:  hbPulseInterval,
			nowOffset: 25 * time.Minute,
			wantPoint: 10 * time.Minute,
		},
		{
			name:      "exactly at second pulse (40m)",
			interval:  hbPulseInterval,
			nowOffset: 40 * time.Minute,
			wantPoint: 40 * time.Minute,
		},
		{
			name:      "just past second pulse (41m) — trigger is at +40m",
			interval:  hbPulseInterval,
			nowOffset: 41 * time.Minute,
			wantPoint: 40 * time.Minute,
		},
		{
			name:      "exactly at third pulse (70m)",
			interval:  hbPulseInterval,
			nowOffset: 70 * time.Minute,
			wantPoint: 70 * time.Minute,
		},
		{
			name:      "fast pulse: 20m — trigger is at +20m",
			interval:  hbFastPulse,
			nowOffset: 20 * time.Minute,
			wantPoint: 20 * time.Minute,
		},
		{
			name:      "fast pulse: 25m — trigger is at +20m",
			interval:  hbFastPulse,
			nowOffset: 25 * time.Minute,
			wantPoint: 20 * time.Minute,
		},
		{
			name:      "fast pulse: 30m — trigger is at +30m",
			interval:  hbFastPulse,
			nowOffset: 30 * time.Minute,
			wantPoint: 30 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := base.Add(tt.nowOffset)
			trigger := latestDueTrigger(base, tt.interval, now)

			if tt.wantZero {
				if !trigger.IsZero() {
					t.Errorf("expected zero trigger, got %v", trigger)
				}
				return
			}

			want := base.Add(tt.wantPoint)
			if !trigger.Equal(want) {
				t.Errorf("trigger = %v (offset %v), want %v (offset %v)",
					trigger.Format(time.RFC3339), trigger.Sub(base),
					want.Format(time.RFC3339), tt.wantPoint)
			}
		})
	}
}

func TestLatestDueTriggerFireDecision(t *testing.T) {
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		nowOffset time.Duration
		lastPulse time.Time // zero means never fired
		wantFire  bool
	}{
		{
			name:      "first pulse due, never fired",
			nowOffset: 10 * time.Minute,
			wantFire:  true,
		},
		{
			name:      "first pulse due, already fired",
			nowOffset: 10 * time.Minute,
			lastPulse: base.Add(10 * time.Minute),
			wantFire:  false,
		},
		{
			name:      "between pulses, first already fired",
			nowOffset: 25 * time.Minute,
			lastPulse: base.Add(12 * time.Minute),
			wantFire:  false,
		},
		{
			name:      "second pulse due, first was fired",
			nowOffset: 40 * time.Minute,
			lastPulse: base.Add(12 * time.Minute),
			wantFire:  true,
		},
		{
			name:      "cold start at 40m, never fired — fires immediately",
			nowOffset: 40 * time.Minute,
			wantFire:  true,
		},
		{
			name:      "cold start at 41m, never fired — fires (trigger at +40m)",
			nowOffset: 41 * time.Minute,
			wantFire:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := base.Add(tt.nowOffset)
			trigger := latestDueTrigger(base, hbPulseInterval, now)

			fired := !trigger.IsZero() && trigger.After(tt.lastPulse)
			if fired != tt.wantFire {
				t.Errorf("fire = %v, want %v (trigger=%v, lastPulse=%v)",
					fired, tt.wantFire,
					trigger.Format(time.RFC3339),
					tt.lastPulse.Format(time.RFC3339))
			}
		})
	}
}
