package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// shouldFire simulates the fire decision: given lastActive, interval, now, and lastPulse,
// returns whether a pulse should fire and what the trigger point is.
func shouldFire(lastActive time.Time, interval time.Duration, now time.Time, lastPulse time.Time) (fire bool, trigger time.Time) {
	trigger = latestDueTrigger(lastActive, interval, now)
	if trigger.IsZero() {
		return false, trigger
	}
	return trigger.After(lastPulse), trigger
}

// TestScenarioNormalPulseSequence simulates a user going quiet and pulses firing
// at +10m, +40m, +70m with 30s scans in between.
func TestScenarioNormalPulseSequence(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval
	var lastPulse time.Time // never fired

	// Scan at +5m: too early
	fire, _ := shouldFire(lastActive, interval, lastActive.Add(5*time.Minute), lastPulse)
	assertFire(t, "scan@+5m", fire, false)

	// Scan at +9m30s: still too early
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(9*time.Minute+30*time.Second), lastPulse)
	assertFire(t, "scan@+9m30s", fire, false)

	// Scan at +10m: first pulse fires
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(10*time.Minute), lastPulse)
	assertFire(t, "scan@+10m", fire, true)
	lastPulse = lastActive.Add(10 * time.Minute) // record fire time

	// Scan at +10m30s: same cycle, deduped
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(10*time.Minute+30*time.Second), lastPulse)
	assertFire(t, "scan@+10m30s (dedup)", fire, false)

	// Scans at +20m, +30m: between pulses, no fire
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(20*time.Minute), lastPulse)
	assertFire(t, "scan@+20m", fire, false)
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(30*time.Minute), lastPulse)
	assertFire(t, "scan@+30m", fire, false)

	// Scan at +40m: second pulse fires
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(40*time.Minute), lastPulse)
	assertFire(t, "scan@+40m", fire, true)
	lastPulse = lastActive.Add(40 * time.Minute)

	// Scan at +40m30s: deduped
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(40*time.Minute+30*time.Second), lastPulse)
	assertFire(t, "scan@+40m30s (dedup)", fire, false)

	// Scan at +70m: third pulse fires
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(70*time.Minute), lastPulse)
	assertFire(t, "scan@+70m", fire, true)
	lastPulse = lastActive.Add(70 * time.Minute)

	// Scan at +100m: fourth pulse
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(100*time.Minute), lastPulse)
	assertFire(t, "scan@+100m", fire, true)
}

// TestScenarioUserSendsMessageMidCycle simulates the user sending a new message
// which moves the entire trigger timeline.
func TestScenarioUserSendsMessageMidCycle(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval
	var lastPulse time.Time

	// First pulse fires at +10m
	fire, _ := shouldFire(lastActive, interval, lastActive.Add(10*time.Minute), lastPulse)
	assertFire(t, "first pulse@+10m", fire, true)
	lastPulse = lastActive.Add(10 * time.Minute)

	// At +25m, user sends a new message → lastActive shifts
	newLastActive := lastActive.Add(25 * time.Minute)

	// Scan at +30m (5m after new message): too early on new timeline
	fire, _ = shouldFire(newLastActive, interval, lastActive.Add(30*time.Minute), lastPulse)
	assertFire(t, "scan@+30m (5m quiet on new timeline)", fire, false)

	// Scan at +35m (10m after new message): first pulse on new timeline fires
	fire, trigger := shouldFire(newLastActive, interval, lastActive.Add(35*time.Minute), lastPulse)
	assertFire(t, "scan@+35m (10m quiet on new timeline)", fire, true)
	wantTrigger := newLastActive.Add(10 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger point = %v, want %v (newLastActive+10m)", trigger, wantTrigger)
	}
	lastPulse = lastActive.Add(35 * time.Minute)

	// Next pulse on new timeline: newLastActive+40m = original+65m
	fire, _ = shouldFire(newLastActive, interval, lastActive.Add(65*time.Minute), lastPulse)
	assertFire(t, "scan@+65m (newLastActive+40m)", fire, true)
}

// TestScenarioRestartWithPersistedState simulates program restart.
// lastPulse is loaded from disk, pulses continue without double-firing.
func TestScenarioRestartWithPersistedState(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval

	// First pulse fired at +10m, persisted lastPulse.
	lastPulse := lastActive.Add(10 * time.Minute)

	// === RESTART at +20m === (state loaded from disk)

	// Scan at +20m: between pulses, no fire
	fire, _ := shouldFire(lastActive, interval, lastActive.Add(20*time.Minute), lastPulse)
	assertFire(t, "post-restart scan@+20m", fire, false)

	// Scan at +30m: still between pulses
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(30*time.Minute), lastPulse)
	assertFire(t, "post-restart scan@+30m", fire, false)

	// Scan at +40m: second pulse fires
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(40*time.Minute), lastPulse)
	assertFire(t, "post-restart scan@+40m", fire, true)
}

// TestScenarioRestartNeverFired simulates program restart with no persisted state
// (clean install or state file missing). lastPulse is zero.
func TestScenarioRestartNeverFired(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval
	var lastPulse time.Time // zero — no state file

	// Program starts at +50m. Latest trigger point is +40m.
	// Since lastPulse is zero, +40m > zero → fires immediately.
	fire, trigger := shouldFire(lastActive, interval, lastActive.Add(50*time.Minute), lastPulse)
	assertFire(t, "cold start@+50m", fire, true)
	// Trigger point should be +40m (the latest trigger <= now)
	wantTrigger := lastActive.Add(40 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = offset %v, want offset +40m", trigger.Sub(lastActive))
	}
}

// TestScenarioProgramDownForHours simulates the program being off for 3 hours
// then starting. Should fire exactly once (the latest trigger point), not
// catch up all missed pulses.
func TestScenarioProgramDownForHours(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval

	// First pulse fired at +10m.
	lastPulse := lastActive.Add(10 * time.Minute)

	// === Program down from +15m to +200m (3h20m) ===

	// Program restarts at +200m. Trigger points: +10, +40, +70, +100, +130, +160, +190.
	// Latest trigger <= +200m is +190m.
	fire, trigger := shouldFire(lastActive, interval, lastActive.Add(200*time.Minute), lastPulse)
	assertFire(t, "restart@+200m after 3h down", fire, true)
	wantTrigger := lastActive.Add(190 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = offset %v, want offset +190m", trigger.Sub(lastActive))
	}
	lastPulse = lastActive.Add(200 * time.Minute) // record

	// After firing, next trigger is +220m. Scan at +210m: no fire.
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(210*time.Minute), lastPulse)
	assertFire(t, "scan@+210m after catch-up", fire, false)

	// Scan at +220m: fires (trigger point +220m)
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(220*time.Minute), lastPulse)
	assertFire(t, "scan@+220m", fire, true)
}

// TestScenarioThreadRunningBlocksPulse simulates a pulse being skipped because
// the thread is running, then firing on the next scan after thread completes.
func TestScenarioThreadRunningBlocksPulse(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval
	var lastPulse time.Time

	// At +10m: thread is running → scan skips (caller responsibility).
	// At +10m30s: thread finishes, scan runs.
	fire, trigger := shouldFire(lastActive, interval, lastActive.Add(10*time.Minute+30*time.Second), lastPulse)
	assertFire(t, "scan@+10m30s after thread done", fire, true)
	// Trigger point is still +10m.
	wantTrigger := lastActive.Add(10 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = offset %v, want offset +10m", trigger.Sub(lastActive))
	}
	lastPulse = lastActive.Add(10*time.Minute + 30*time.Second)

	// At +40m: next pulse fires normally.
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(40*time.Minute), lastPulse)
	assertFire(t, "scan@+40m", fire, true)
}

// TestScenarioThreadRunningAcrossTwoCycles simulates a long-running thread that
// spans multiple trigger cycles. Only fires once when it finishes.
func TestScenarioThreadRunningAcrossTwoCycles(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval
	var lastPulse time.Time

	// First pulse fires at +10m.
	fire, _ := shouldFire(lastActive, interval, lastActive.Add(10*time.Minute), lastPulse)
	assertFire(t, "first pulse@+10m", fire, true)
	lastPulse = lastActive.Add(10 * time.Minute)

	// Thread starts running at +38m, runs until +55m.
	// At +40m: trigger due but thread running → skipped by caller.
	// At +55m: thread done, scan runs. Trigger point is +40m.
	fire, trigger := shouldFire(lastActive, interval, lastActive.Add(55*time.Minute), lastPulse)
	assertFire(t, "scan@+55m after long thread", fire, true)
	wantTrigger := lastActive.Add(40 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = offset %v, want offset +40m", trigger.Sub(lastActive))
	}
	lastPulse = lastActive.Add(55 * time.Minute)

	// Next pulse at +70m fires normally.
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(70*time.Minute), lastPulse)
	assertFire(t, "scan@+70m", fire, true)
}

// TestScenarioFastPulseSwitch simulates heartbeat.md being modified,
// switching to fast pulse (10m intervals), then switching back.
func TestScenarioFastPulseSwitch(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// Normal first pulse at +10m.
	fire, _ := shouldFire(lastActive, hbPulseInterval, lastActive.Add(10*time.Minute), lastPulse)
	assertFire(t, "normal pulse@+10m", fire, true)
	lastPulse = lastActive.Add(10 * time.Minute)

	// LLM modified heartbeat.md → fast pulse (10m interval).
	// Fast timeline: +10, +20, +30, +40, ...
	// At +15m: between +10 and +20, no fire.
	fire, _ = shouldFire(lastActive, hbFastPulse, lastActive.Add(15*time.Minute), lastPulse)
	assertFire(t, "fast scan@+15m", fire, false)

	// At +20m: trigger +20m fires.
	fire, _ = shouldFire(lastActive, hbFastPulse, lastActive.Add(20*time.Minute), lastPulse)
	assertFire(t, "fast pulse@+20m", fire, true)
	lastPulse = lastActive.Add(20 * time.Minute)

	// At +30m: trigger +30m fires.
	fire, _ = shouldFire(lastActive, hbFastPulse, lastActive.Add(30*time.Minute), lastPulse)
	assertFire(t, "fast pulse@+30m", fire, true)
	lastPulse = lastActive.Add(30 * time.Minute)

	// heartbeat.md not modified this time → back to normal (30m interval).
	// Normal timeline: +10, +40, +70. Latest trigger at +35m is +10m.
	// +10m is not after lastPulse (+30m) → no fire.
	fire, _ = shouldFire(lastActive, hbPulseInterval, lastActive.Add(35*time.Minute), lastPulse)
	assertFire(t, "normal scan@+35m", fire, false)

	// At +40m: trigger +40m fires.
	fire, _ = shouldFire(lastActive, hbPulseInterval, lastActive.Add(40*time.Minute), lastPulse)
	assertFire(t, "normal pulse@+40m", fire, true)
}

// TestScenarioUserMessageResetsTimeline simulates multiple user messages
// shifting the trigger timeline each time.
func TestScenarioUserMessageResetsTimeline(t *testing.T) {
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// User message at 12:00.
	lastActive := baseTime

	// First pulse at 12:10.
	fire, _ := shouldFire(lastActive, hbPulseInterval, baseTime.Add(10*time.Minute), lastPulse)
	assertFire(t, "pulse#1@12:10", fire, true)
	lastPulse = baseTime.Add(10 * time.Minute)

	// User sends message at 12:15 → new lastActive.
	lastActive = baseTime.Add(15 * time.Minute)

	// At 12:20 (5m quiet): too early on new timeline.
	fire, _ = shouldFire(lastActive, hbPulseInterval, baseTime.Add(20*time.Minute), lastPulse)
	assertFire(t, "scan@12:20 (5m quiet)", fire, false)

	// At 12:25 (10m quiet): fires on new timeline.
	fire, _ = shouldFire(lastActive, hbPulseInterval, baseTime.Add(25*time.Minute), lastPulse)
	assertFire(t, "pulse#2@12:25", fire, true)
	lastPulse = baseTime.Add(25 * time.Minute)

	// User sends message at 12:30 → new lastActive.
	lastActive = baseTime.Add(30 * time.Minute)

	// At 12:40 (10m quiet): fires.
	fire, _ = shouldFire(lastActive, hbPulseInterval, baseTime.Add(40*time.Minute), lastPulse)
	assertFire(t, "pulse#3@12:40", fire, true)
	lastPulse = baseTime.Add(40 * time.Minute)

	// No more user messages. Next pulse at newLastActive+40m = 13:10.
	fire, _ = shouldFire(lastActive, hbPulseInterval, baseTime.Add(60*time.Minute), lastPulse)
	assertFire(t, "scan@13:00 (30m quiet)", fire, false)

	fire, _ = shouldFire(lastActive, hbPulseInterval, baseTime.Add(70*time.Minute), lastPulse)
	assertFire(t, "pulse#4@13:10", fire, true)
}

// TestScenarioRapidUserMessages simulates user sending many messages in quick
// succession. Each one resets the timeline, so no pulse fires until 10m quiet.
func TestScenarioRapidUserMessages(t *testing.T) {
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// User sends messages at 12:00, 12:05, 12:08, 12:15.
	// Scan happens every 30s but we only care about whether fire would happen.

	// At 12:10 with lastActive=12:08: only 2m quiet → no fire.
	lastActive := baseTime.Add(8 * time.Minute)
	fire, _ := shouldFire(lastActive, hbPulseInterval, baseTime.Add(10*time.Minute), lastPulse)
	assertFire(t, "scan@12:10 (lastActive=12:08)", fire, false)

	// User message at 12:15. lastActive=12:15.
	lastActive = baseTime.Add(15 * time.Minute)

	// At 12:20: 5m quiet → no fire.
	fire, _ = shouldFire(lastActive, hbPulseInterval, baseTime.Add(20*time.Minute), lastPulse)
	assertFire(t, "scan@12:20 (lastActive=12:15)", fire, false)

	// At 12:25: 10m quiet → fires.
	fire, _ = shouldFire(lastActive, hbPulseInterval, baseTime.Add(25*time.Minute), lastPulse)
	assertFire(t, "pulse@12:25 (lastActive=12:15)", fire, true)
}

// TestScenarioRestart48hBoundary tests that no pulse fires if user was inactive > 48h.
// This is handled by the caller (scan method), but we verify the trigger math.
func TestScenario48hBoundary(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval
	var lastPulse time.Time

	// At +47h: still within window. Trigger point exists.
	now := lastActive.Add(47 * time.Hour)
	fire, trigger := shouldFire(lastActive, interval, now, lastPulse)
	assertFire(t, "scan@+47h", fire, true)
	if trigger.IsZero() {
		t.Error("expected non-zero trigger at +47h")
	}

	// The 48h cutoff is enforced by the caller (quiet > hbActivityWindow),
	// not by latestDueTrigger. The function itself still returns valid triggers.
	now = lastActive.Add(49 * time.Hour)
	trigger = latestDueTrigger(lastActive, interval, now)
	if trigger.IsZero() {
		t.Error("latestDueTrigger should return a trigger even past 48h — caller enforces cutoff")
	}
}

// TestScenarioProgramNeverRanBefore simulates first-ever startup with no state.
// User sent a message 2 hours ago.
func TestScenarioProgramNeverRanBefore(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval
	var lastPulse time.Time // zero — never ran

	// Program starts at +120m. Many trigger points missed: +10, +40, +70, +100.
	// Latest trigger <= +120m is +100m. Should fire once.
	now := lastActive.Add(120 * time.Minute)
	fire, trigger := shouldFire(lastActive, interval, now, lastPulse)
	assertFire(t, "first-ever start@+120m", fire, true)
	wantTrigger := lastActive.Add(100 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = offset %v, want offset +100m", trigger.Sub(lastActive))
	}
	lastPulse = now

	// Next fires at +130m (next trigger point).
	fire, _ = shouldFire(lastActive, interval, lastActive.Add(125*time.Minute), lastPulse)
	assertFire(t, "scan@+125m", fire, false)

	fire, _ = shouldFire(lastActive, interval, lastActive.Add(130*time.Minute), lastPulse)
	assertFire(t, "scan@+130m", fire, true)
}

// TestScenarioMultipleScansPerCycle verifies that repeated 30s scans within the
// same trigger cycle only fire once.
func TestScenarioMultipleScansPerCycle(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval
	var lastPulse time.Time

	// Simulate 30s scans from +10m to +39m30s.
	fireCount := 0
	for offset := 10 * time.Minute; offset < 40*time.Minute; offset += 30 * time.Second {
		fire, _ := shouldFire(lastActive, interval, lastActive.Add(offset), lastPulse)
		if fire {
			fireCount++
			lastPulse = lastActive.Add(offset)
		}
	}
	if fireCount != 1 {
		t.Errorf("expected exactly 1 fire in [+10m, +40m), got %d", fireCount)
	}

	// Second cycle: +40m to +69m30s.
	fireCount = 0
	for offset := 40 * time.Minute; offset < 70*time.Minute; offset += 30 * time.Second {
		fire, _ := shouldFire(lastActive, interval, lastActive.Add(offset), lastPulse)
		if fire {
			fireCount++
			lastPulse = lastActive.Add(offset)
		}
	}
	if fireCount != 1 {
		t.Errorf("expected exactly 1 fire in [+40m, +70m), got %d", fireCount)
	}
}

// TestScenarioUserMessageBetweenPulses simulates a user message arriving between
// two trigger points. The new timeline should fire at newLastActive+10m.
func TestScenarioUserMessageBetweenPulses(t *testing.T) {
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval
	var lastPulse time.Time

	// First pulse at +10m.
	lastActive := baseTime
	fire, _ := shouldFire(lastActive, interval, baseTime.Add(10*time.Minute), lastPulse)
	assertFire(t, "pulse@+10m", fire, true)
	lastPulse = baseTime.Add(10 * time.Minute)

	// User sends message at +35m (between +10m and +40m triggers).
	lastActive = baseTime.Add(35 * time.Minute)

	// Old timeline's +40m trigger: on new timeline, +40m = newLastActive+5m → no fire.
	fire, _ = shouldFire(lastActive, interval, baseTime.Add(40*time.Minute), lastPulse)
	assertFire(t, "scan@+40m (newLastActive+5m)", fire, false)

	// newLastActive+10m = +45m → fires.
	fire, trigger := shouldFire(lastActive, interval, baseTime.Add(45*time.Minute), lastPulse)
	assertFire(t, "pulse@+45m (newLastActive+10m)", fire, true)
	wantTrigger := lastActive.Add(10 * time.Minute) // = baseTime+45m
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = %v, want %v", trigger, wantTrigger)
	}
}

// TestStatePersistence verifies that state can be saved and reloaded from disk.
func TestStatePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "heartbeat-state.json")

	now := time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC)
	hbMtime := time.Date(2025, 1, 1, 12, 25, 0, 0, time.UTC)

	// Create and populate state.
	sessions := map[string]*hbSessionState{
		"telegram:123": {LastPulse: now, LastHBMtime: hbMtime},
		"discord:456":  {LastPulse: now.Add(-10 * time.Minute)},
	}

	// Save.
	data, err := json.Marshal(sessions)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Reload.
	loaded, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded map[string]*hbSessionState
	if err := json.Unmarshal(loaded, &reloaded); err != nil {
		t.Fatal(err)
	}

	// Verify telegram:123.
	st := reloaded["telegram:123"]
	if st == nil {
		t.Fatal("telegram:123 not found after reload")
	}
	if !st.LastPulse.Equal(now) {
		t.Errorf("LastPulse = %v, want %v", st.LastPulse, now)
	}
	if !st.LastHBMtime.Equal(hbMtime) {
		t.Errorf("LastHBMtime = %v, want %v", st.LastHBMtime, hbMtime)
	}

	// Verify discord:456 (no hbMtime → zero).
	st2 := reloaded["discord:456"]
	if st2 == nil {
		t.Fatal("discord:456 not found after reload")
	}
	if !st2.LastHBMtime.IsZero() {
		t.Errorf("expected zero LastHBMtime for discord:456, got %v", st2.LastHBMtime)
	}
}

// TestStatePersistedPulseDedup verifies that persisted lastPulse prevents
// duplicate firing after restart.
func TestStatePersistedPulseDedup(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := hbPulseInterval

	// Pulse fired at +10m, persisted.
	lastPulse := lastActive.Add(10 * time.Minute)

	// === RESTART ===

	// Simulate multiple scans at +10m30s, +11m, +11m30s — all should be deduped.
	for _, offset := range []time.Duration{
		10*time.Minute + 30*time.Second,
		11 * time.Minute,
		11*time.Minute + 30*time.Second,
		20 * time.Minute,
		39*time.Minute + 30*time.Second,
	} {
		fire, _ := shouldFire(lastActive, interval, lastActive.Add(offset), lastPulse)
		if fire {
			t.Errorf("unexpected fire at +%v with lastPulse=+10m", offset)
		}
	}

	// At +40m: fires.
	fire, _ := shouldFire(lastActive, interval, lastActive.Add(40*time.Minute), lastPulse)
	assertFire(t, "post-restart pulse@+40m", fire, true)
}

// TestScenarioFastToNormalTransition tests the exact transition point when
// switching from fast pulse back to normal pulse.
func TestScenarioFastToNormalTransition(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// Pulse at +10m (normal).
	fire, _ := shouldFire(lastActive, hbPulseInterval, lastActive.Add(10*time.Minute), lastPulse)
	assertFire(t, "pulse@+10m", fire, true)
	lastPulse = lastActive.Add(10 * time.Minute)

	// Switch to fast. Pulse at +20m.
	fire, _ = shouldFire(lastActive, hbFastPulse, lastActive.Add(20*time.Minute), lastPulse)
	assertFire(t, "fast@+20m", fire, true)
	lastPulse = lastActive.Add(20 * time.Minute)

	// Switch back to normal. Timeline: +10, +40, +70.
	// Latest trigger at +25m is +10m. +10m is not after lastPulse (+20m) → no fire.
	fire, _ = shouldFire(lastActive, hbPulseInterval, lastActive.Add(25*time.Minute), lastPulse)
	assertFire(t, "normal@+25m", fire, false)

	// At +40m: trigger +40m, after lastPulse (+20m) → fires.
	fire, _ = shouldFire(lastActive, hbPulseInterval, lastActive.Add(40*time.Minute), lastPulse)
	assertFire(t, "normal@+40m", fire, true)
}

func assertFire(t *testing.T, label string, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("%s: fire = %v, want %v", label, got, want)
	}
}
