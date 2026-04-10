package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Trigger schedule with hbQuietMin=15m, hbPulseInterval=45m, hbPulseGrowth=30m:
//   T1: +15m
//   T2: +60m  (15+45)
//   T3: +135m (60+75)
//   T4: +240m (135+105)
//   T5: +375m (240+135)

func TestLatestDueTrigger(t *testing.T) {
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		nowOffset    time.Duration
		wantZero     bool
		wantPoint    time.Duration // expected trigger offset from base
		wantInterval time.Duration // expected interval to next trigger
	}{
		{
			name:      "not quiet enough (5m)",
			nowOffset: 5 * time.Minute,
			wantZero:  true,
		},
		{
			name:         "exactly at first pulse (15m)",
			nowOffset:    hbQuietMin,
			wantPoint:    15 * time.Minute,
			wantInterval: 45 * time.Minute,
		},
		{
			name:         "just past first pulse (16m)",
			nowOffset:    16 * time.Minute,
			wantPoint:    15 * time.Minute,
			wantInterval: 45 * time.Minute,
		},
		{
			name:         "between first and second (30m)",
			nowOffset:    30 * time.Minute,
			wantPoint:    15 * time.Minute,
			wantInterval: 45 * time.Minute,
		},
		{
			name:         "exactly at second pulse (60m)",
			nowOffset:    60 * time.Minute,
			wantPoint:    60 * time.Minute,
			wantInterval: 75 * time.Minute,
		},
		{
			name:         "just past second pulse (61m)",
			nowOffset:    61 * time.Minute,
			wantPoint:    60 * time.Minute,
			wantInterval: 75 * time.Minute,
		},
		{
			name:         "exactly at third pulse (135m)",
			nowOffset:    135 * time.Minute,
			wantPoint:    135 * time.Minute,
			wantInterval: 105 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := base.Add(tt.nowOffset)
			trigger, nextInterval, _ := latestDueTrigger(base, now)

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
			if nextInterval != tt.wantInterval {
				t.Errorf("nextInterval = %v, want %v", nextInterval, tt.wantInterval)
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
			nowOffset: 15 * time.Minute,
			wantFire:  true,
		},
		{
			name:      "first pulse due, already fired",
			nowOffset: 15 * time.Minute,
			lastPulse: base.Add(15 * time.Minute),
			wantFire:  false,
		},
		{
			name:      "between pulses, first already fired",
			nowOffset: 30 * time.Minute,
			lastPulse: base.Add(17 * time.Minute),
			wantFire:  false,
		},
		{
			name:      "second pulse due, first was fired",
			nowOffset: 60 * time.Minute,
			lastPulse: base.Add(17 * time.Minute),
			wantFire:  true,
		},
		{
			name:      "cold start at 60m, never fired",
			nowOffset: 60 * time.Minute,
			wantFire:  true,
		},
		{
			name:      "cold start at 61m, never fired",
			nowOffset: 61 * time.Minute,
			wantFire:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := base.Add(tt.nowOffset)
			trigger, _, _ := latestDueTrigger(base, now)

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

// shouldFire simulates the fire decision: given lastActive, now, and lastPulse,
// returns whether a pulse should fire and what the trigger point is.
func shouldFire(lastActive time.Time, now time.Time, lastPulse time.Time) (fire bool, trigger time.Time) {
	trigger, _, _ = latestDueTrigger(lastActive, now)
	if trigger.IsZero() {
		return false, trigger
	}
	return trigger.After(lastPulse), trigger
}

// TestScenarioNormalPulseSequence simulates a user going quiet and pulses firing
// at +15m, +60m, +135m, +240m with 30s scans in between.
func TestScenarioNormalPulseSequence(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time // never fired

	// Scan at +5m: too early
	fire, _ := shouldFire(lastActive, lastActive.Add(5*time.Minute), lastPulse)
	assertFire(t, "scan@+5m", fire, false)

	// Scan at +14m30s: still too early
	fire, _ = shouldFire(lastActive, lastActive.Add(14*time.Minute+30*time.Second), lastPulse)
	assertFire(t, "scan@+14m30s", fire, false)

	// Scan at +15m: first pulse fires
	fire, _ = shouldFire(lastActive, lastActive.Add(15*time.Minute), lastPulse)
	assertFire(t, "scan@+15m", fire, true)
	lastPulse = lastActive.Add(15 * time.Minute) // record fire time

	// Scan at +15m30s: same cycle, deduped
	fire, _ = shouldFire(lastActive, lastActive.Add(15*time.Minute+30*time.Second), lastPulse)
	assertFire(t, "scan@+15m30s (dedup)", fire, false)

	// Scans at +30m, +50m: between pulses, no fire
	fire, _ = shouldFire(lastActive, lastActive.Add(30*time.Minute), lastPulse)
	assertFire(t, "scan@+30m", fire, false)
	fire, _ = shouldFire(lastActive, lastActive.Add(50*time.Minute), lastPulse)
	assertFire(t, "scan@+50m", fire, false)

	// Scan at +60m: second pulse fires (interval=45m)
	fire, _ = shouldFire(lastActive, lastActive.Add(60*time.Minute), lastPulse)
	assertFire(t, "scan@+60m", fire, true)
	lastPulse = lastActive.Add(60 * time.Minute)

	// Scan at +60m30s: deduped
	fire, _ = shouldFire(lastActive, lastActive.Add(60*time.Minute+30*time.Second), lastPulse)
	assertFire(t, "scan@+60m30s (dedup)", fire, false)

	// Scan at +135m: third pulse fires (interval=75m)
	fire, _ = shouldFire(lastActive, lastActive.Add(135*time.Minute), lastPulse)
	assertFire(t, "scan@+135m", fire, true)
	lastPulse = lastActive.Add(135 * time.Minute)

	// Scan at +240m: fourth pulse (interval=105m)
	fire, _ = shouldFire(lastActive, lastActive.Add(240*time.Minute), lastPulse)
	assertFire(t, "scan@+240m", fire, true)
}

// TestScenarioUserSendsMessageMidCycle simulates the user sending a new message
// which moves the entire trigger timeline.
func TestScenarioUserSendsMessageMidCycle(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// First pulse fires at +15m
	fire, _ := shouldFire(lastActive, lastActive.Add(15*time.Minute), lastPulse)
	assertFire(t, "first pulse@+15m", fire, true)
	lastPulse = lastActive.Add(15 * time.Minute)

	// At +30m, user sends a new message → lastActive shifts
	newLastActive := lastActive.Add(30 * time.Minute)

	// Scan at +40m (10m after new message): too early on new timeline (need 15m)
	fire, _ = shouldFire(newLastActive, lastActive.Add(40*time.Minute), lastPulse)
	assertFire(t, "scan@+40m (10m quiet on new timeline)", fire, false)

	// Scan at +45m (15m after new message): first pulse on new timeline fires
	fire, trigger := shouldFire(newLastActive, lastActive.Add(45*time.Minute), lastPulse)
	assertFire(t, "scan@+45m (15m quiet on new timeline)", fire, true)
	wantTrigger := newLastActive.Add(15 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger point = %v, want %v (newLastActive+15m)", trigger, wantTrigger)
	}
	lastPulse = lastActive.Add(45 * time.Minute)

	// Next pulse on new timeline: newLastActive+60m = original+90m
	fire, _ = shouldFire(newLastActive, lastActive.Add(90*time.Minute), lastPulse)
	assertFire(t, "scan@+90m (newLastActive+60m)", fire, true)
}

// TestScenarioRestartWithPersistedState simulates program restart.
// lastPulse is loaded from disk, pulses continue without double-firing.
func TestScenarioRestartWithPersistedState(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// First pulse fired at +15m, persisted lastPulse.
	lastPulse := lastActive.Add(15 * time.Minute)

	// === RESTART at +30m === (state loaded from disk)

	// Scan at +30m: between pulses, no fire
	fire, _ := shouldFire(lastActive, lastActive.Add(30*time.Minute), lastPulse)
	assertFire(t, "post-restart scan@+30m", fire, false)

	// Scan at +50m: still between pulses
	fire, _ = shouldFire(lastActive, lastActive.Add(50*time.Minute), lastPulse)
	assertFire(t, "post-restart scan@+50m", fire, false)

	// Scan at +60m: second pulse fires
	fire, _ = shouldFire(lastActive, lastActive.Add(60*time.Minute), lastPulse)
	assertFire(t, "post-restart scan@+60m", fire, true)
}

// TestScenarioRestartNeverFired simulates program restart with no persisted state
// (clean install or state file missing). lastPulse is zero.
func TestScenarioRestartNeverFired(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time // zero — no state file

	// Program starts at +50m. Latest trigger point is +15m.
	// Since lastPulse is zero, +15m > zero → fires immediately.
	fire, trigger := shouldFire(lastActive, lastActive.Add(50*time.Minute), lastPulse)
	assertFire(t, "cold start@+50m", fire, true)
	// Trigger point should be +15m (the latest trigger <= now, before +60m)
	wantTrigger := lastActive.Add(15 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = offset %v, want offset +15m", trigger.Sub(lastActive))
	}
}

// TestScenarioProgramDownForHours simulates the program being off for 3+ hours
// then starting. Should fire exactly once (the latest trigger point), not
// catch up all missed pulses.
func TestScenarioProgramDownForHours(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// First pulse fired at +15m.
	lastPulse := lastActive.Add(15 * time.Minute)

	// === Program down from +20m to +200m ===
	// Trigger points: +15, +60, +135, +240. Latest <= +200m is +135m.
	fire, trigger := shouldFire(lastActive, lastActive.Add(200*time.Minute), lastPulse)
	assertFire(t, "restart@+200m after long down", fire, true)
	wantTrigger := lastActive.Add(135 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = offset %v, want offset +135m", trigger.Sub(lastActive))
	}
	lastPulse = lastActive.Add(200 * time.Minute) // record

	// After firing, next trigger is +240m. Scan at +230m: no fire.
	fire, _ = shouldFire(lastActive, lastActive.Add(230*time.Minute), lastPulse)
	assertFire(t, "scan@+230m after catch-up", fire, false)

	// Scan at +240m: fires (trigger point +240m)
	fire, _ = shouldFire(lastActive, lastActive.Add(240*time.Minute), lastPulse)
	assertFire(t, "scan@+240m", fire, true)
}

// TestScenarioThreadRunningBlocksPulse simulates a pulse being skipped because
// the thread is running, then firing on the next scan after thread completes.
func TestScenarioThreadRunningBlocksPulse(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// At +15m: thread is running → scan skips (caller responsibility).
	// At +15m30s: thread finishes, scan runs.
	fire, trigger := shouldFire(lastActive, lastActive.Add(15*time.Minute+30*time.Second), lastPulse)
	assertFire(t, "scan@+15m30s after thread done", fire, true)
	// Trigger point is still +15m.
	wantTrigger := lastActive.Add(15 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = offset %v, want offset +15m", trigger.Sub(lastActive))
	}
	lastPulse = lastActive.Add(15*time.Minute + 30*time.Second)

	// At +60m: next pulse fires normally.
	fire, _ = shouldFire(lastActive, lastActive.Add(60*time.Minute), lastPulse)
	assertFire(t, "scan@+60m", fire, true)
}

// TestScenarioThreadRunningAcrossTwoCycles simulates a long-running thread that
// spans multiple trigger cycles. Only fires once when it finishes.
func TestScenarioThreadRunningAcrossTwoCycles(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// First pulse fires at +15m.
	fire, _ := shouldFire(lastActive, lastActive.Add(15*time.Minute), lastPulse)
	assertFire(t, "first pulse@+15m", fire, true)
	lastPulse = lastActive.Add(15 * time.Minute)

	// Thread starts running at +58m, runs until +75m.
	// At +60m: trigger due but thread running → skipped by caller.
	// At +75m: thread done, scan runs. Trigger point is +60m.
	fire, trigger := shouldFire(lastActive, lastActive.Add(75*time.Minute), lastPulse)
	assertFire(t, "scan@+75m after long thread", fire, true)
	wantTrigger := lastActive.Add(60 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = offset %v, want offset +60m", trigger.Sub(lastActive))
	}
	lastPulse = lastActive.Add(75 * time.Minute)

	// Next pulse at +135m fires normally.
	fire, _ = shouldFire(lastActive, lastActive.Add(135*time.Minute), lastPulse)
	assertFire(t, "scan@+135m", fire, true)
}

// TestScenarioUserMessageResetsTimeline simulates multiple user messages
// shifting the trigger timeline each time.
func TestScenarioUserMessageResetsTimeline(t *testing.T) {
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// User message at 12:00.
	lastActive := baseTime

	// First pulse at 12:15.
	fire, _ := shouldFire(lastActive, baseTime.Add(15*time.Minute), lastPulse)
	assertFire(t, "pulse#1@12:15", fire, true)
	lastPulse = baseTime.Add(15 * time.Minute)

	// User sends message at 12:20 → new lastActive.
	lastActive = baseTime.Add(20 * time.Minute)

	// At 12:30 (10m quiet): too early on new timeline (need 15m).
	fire, _ = shouldFire(lastActive, baseTime.Add(30*time.Minute), lastPulse)
	assertFire(t, "scan@12:30 (10m quiet)", fire, false)

	// At 12:35 (15m quiet): fires on new timeline.
	fire, _ = shouldFire(lastActive, baseTime.Add(35*time.Minute), lastPulse)
	assertFire(t, "pulse#2@12:35", fire, true)
	lastPulse = baseTime.Add(35 * time.Minute)

	// User sends message at 12:40 → new lastActive.
	lastActive = baseTime.Add(40 * time.Minute)

	// At 12:55 (15m quiet): fires.
	fire, _ = shouldFire(lastActive, baseTime.Add(55*time.Minute), lastPulse)
	assertFire(t, "pulse#3@12:55", fire, true)
	lastPulse = baseTime.Add(55 * time.Minute)

	// No more user messages. Next pulse at newLastActive+60m = 13:40 (base+100m).
	fire, _ = shouldFire(lastActive, baseTime.Add(90*time.Minute), lastPulse)
	assertFire(t, "scan@13:30 (50m quiet)", fire, false)

	fire, _ = shouldFire(lastActive, baseTime.Add(100*time.Minute), lastPulse)
	assertFire(t, "pulse#4@13:40", fire, true)
}

// TestScenarioRapidUserMessages simulates user sending many messages in quick
// succession. Each one resets the timeline, so no pulse fires until 15m quiet.
func TestScenarioRapidUserMessages(t *testing.T) {
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// User sends messages at 12:00, 12:05, 12:08, 12:15.
	// Scan happens every 30s but we only care about whether fire would happen.

	// At 12:20 with lastActive=12:08: 12m quiet → no fire (need 15m).
	lastActive := baseTime.Add(8 * time.Minute)
	fire, _ := shouldFire(lastActive, baseTime.Add(20*time.Minute), lastPulse)
	assertFire(t, "scan@12:20 (lastActive=12:08)", fire, false)

	// User message at 12:15. lastActive=12:15.
	lastActive = baseTime.Add(15 * time.Minute)

	// At 12:25: 10m quiet → no fire (need 15m).
	fire, _ = shouldFire(lastActive, baseTime.Add(25*time.Minute), lastPulse)
	assertFire(t, "scan@12:25 (lastActive=12:15)", fire, false)

	// At 12:30: 15m quiet → fires.
	fire, _ = shouldFire(lastActive, baseTime.Add(30*time.Minute), lastPulse)
	assertFire(t, "pulse@12:30 (lastActive=12:15)", fire, true)
}

// TestScenario48hBoundary tests that no pulse fires if user was inactive > 48h.
// This is handled by the caller (scan method), but we verify the trigger math.
func TestScenario48hBoundary(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// At +47h: still within window. Trigger point exists.
	now := lastActive.Add(47 * time.Hour)
	fire, trigger := shouldFire(lastActive, now, lastPulse)
	assertFire(t, "scan@+47h", fire, true)
	if trigger.IsZero() {
		t.Error("expected non-zero trigger at +47h")
	}

	// The 48h cutoff is enforced by the caller (quiet > hbActivityWindow),
	// not by latestDueTrigger. The function itself still returns valid triggers.
	now = lastActive.Add(49 * time.Hour)
	trigger, _, _ = latestDueTrigger(lastActive, now)
	if trigger.IsZero() {
		t.Error("latestDueTrigger should return a trigger even past 48h — caller enforces cutoff")
	}
}

// TestScenarioProgramNeverRanBefore simulates first-ever startup with no state.
// User sent a message 2 hours ago.
func TestScenarioProgramNeverRanBefore(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time // zero — never ran

	// Program starts at +120m. Trigger points: +15, +60, +135...
	// Latest trigger <= +120m is +60m. Should fire once.
	now := lastActive.Add(120 * time.Minute)
	fire, trigger := shouldFire(lastActive, now, lastPulse)
	assertFire(t, "first-ever start@+120m", fire, true)
	wantTrigger := lastActive.Add(60 * time.Minute)
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = offset %v, want offset +60m", trigger.Sub(lastActive))
	}
	lastPulse = now

	// Next fires at +135m (60+75).
	fire, _ = shouldFire(lastActive, lastActive.Add(130*time.Minute), lastPulse)
	assertFire(t, "scan@+130m", fire, false)

	fire, _ = shouldFire(lastActive, lastActive.Add(135*time.Minute), lastPulse)
	assertFire(t, "scan@+135m", fire, true)
}

// TestScenarioMultipleScansPerCycle verifies that repeated 30s scans within the
// same trigger cycle only fire once.
func TestScenarioMultipleScansPerCycle(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// Simulate 30s scans from +15m to +59m30s (first cycle).
	fireCount := 0
	for offset := 15 * time.Minute; offset < 60*time.Minute; offset += 30 * time.Second {
		fire, _ := shouldFire(lastActive, lastActive.Add(offset), lastPulse)
		if fire {
			fireCount++
			lastPulse = lastActive.Add(offset)
		}
	}
	if fireCount != 1 {
		t.Errorf("expected exactly 1 fire in [+15m, +60m), got %d", fireCount)
	}

	// Second cycle: +60m to +114m30s (interval=55m).
	fireCount = 0
	for offset := 60 * time.Minute; offset < 115*time.Minute; offset += 30 * time.Second {
		fire, _ := shouldFire(lastActive, lastActive.Add(offset), lastPulse)
		if fire {
			fireCount++
			lastPulse = lastActive.Add(offset)
		}
	}
	if fireCount != 1 {
		t.Errorf("expected exactly 1 fire in [+60m, +115m), got %d", fireCount)
	}
}

// TestScenarioUserMessageBetweenPulses simulates a user message arriving between
// two trigger points. The new timeline should fire at newLastActive+15m.
func TestScenarioUserMessageBetweenPulses(t *testing.T) {
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var lastPulse time.Time

	// First pulse at +15m.
	lastActive := baseTime
	fire, _ := shouldFire(lastActive, baseTime.Add(15*time.Minute), lastPulse)
	assertFire(t, "pulse@+15m", fire, true)
	lastPulse = baseTime.Add(15 * time.Minute)

	// User sends message at +40m (between +15m and +60m triggers).
	lastActive = baseTime.Add(40 * time.Minute)

	// newLastActive+10m = +50m: too early (need 15m quiet)
	fire, _ = shouldFire(lastActive, baseTime.Add(50*time.Minute), lastPulse)
	assertFire(t, "scan@+50m (newLastActive+10m)", fire, false)

	// newLastActive+15m = +55m → fires.
	fire, trigger := shouldFire(lastActive, baseTime.Add(55*time.Minute), lastPulse)
	assertFire(t, "pulse@+55m (newLastActive+15m)", fire, true)
	wantTrigger := lastActive.Add(15 * time.Minute) // = baseTime+55m
	if !trigger.Equal(wantTrigger) {
		t.Errorf("trigger = %v, want %v", trigger, wantTrigger)
	}
}

// TestStatePersistence verifies that state can be saved and reloaded from disk.
func TestStatePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "heartbeat-state.json")

	now := time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC)

	// Create and populate state.
	sessions := map[string]*hbSessionState{
		"telegram:123": {LastPulse: now},
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

	// Verify discord:456.
	st2 := reloaded["discord:456"]
	if st2 == nil {
		t.Fatal("discord:456 not found after reload")
	}
	if !st2.LastPulse.Equal(now.Add(-10 * time.Minute)) {
		t.Errorf("LastPulse = %v, want %v", st2.LastPulse, now.Add(-10*time.Minute))
	}
}

// TestStatePersistedPulseDedup verifies that persisted lastPulse prevents
// duplicate firing after restart.
func TestStatePersistedPulseDedup(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Pulse fired at +15m, persisted.
	lastPulse := lastActive.Add(15 * time.Minute)

	// === RESTART ===

	// Simulate multiple scans at +15m30s, +16m, etc. — all should be deduped.
	for _, offset := range []time.Duration{
		15*time.Minute + 30*time.Second,
		16 * time.Minute,
		16*time.Minute + 30*time.Second,
		30 * time.Minute,
		59*time.Minute + 30*time.Second,
	} {
		fire, _ := shouldFire(lastActive, lastActive.Add(offset), lastPulse)
		if fire {
			t.Errorf("unexpected fire at +%v with lastPulse=+15m", offset)
		}
	}

	// At +60m: fires.
	fire, _ := shouldFire(lastActive, lastActive.Add(60*time.Minute), lastPulse)
	assertFire(t, "post-restart pulse@+60m", fire, true)
}

// TestGrowingIntervals verifies that intervals grow correctly across many cycles.
func TestGrowingIntervals(t *testing.T) {
	lastActive := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Expected trigger points and their next intervals.
	expected := []struct {
		offset       time.Duration
		nextInterval time.Duration
	}{
		{15 * time.Minute, 45 * time.Minute},   // T1
		{60 * time.Minute, 55 * time.Minute},   // T2: 15+45
		{115 * time.Minute, 65 * time.Minute},  // T3: 60+55
		{180 * time.Minute, 75 * time.Minute},  // T4: 115+65
		{255 * time.Minute, 85 * time.Minute},  // T5: 180+75
		{340 * time.Minute, 95 * time.Minute},  // T6: 255+85
		{435 * time.Minute, 105 * time.Minute}, // T7: 340+95
	}

	for i, exp := range expected {
		// Query at exactly the trigger point.
		trigger, nextInterval, _ := latestDueTrigger(lastActive, lastActive.Add(exp.offset))
		wantTrigger := lastActive.Add(exp.offset)
		if !trigger.Equal(wantTrigger) {
			t.Errorf("T%d: trigger = offset %v, want offset %v", i+1, trigger.Sub(lastActive), exp.offset)
		}
		if nextInterval != exp.nextInterval {
			t.Errorf("T%d: nextInterval = %v, want %v", i+1, nextInterval, exp.nextInterval)
		}
	}
}

func assertFire(t *testing.T, label string, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("%s: fire = %v, want %v", label, got, want)
	}
}
