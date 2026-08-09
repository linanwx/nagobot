package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/config"
)

// The roster is the documented timeline: +15m, then gaps of 45/75/105/135/165m.
func TestPulseScheduleMatchesTheDocumentedOffsets(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := newPulseSchedule(anchor)
	want := []time.Duration{
		15 * time.Minute,
		60 * time.Minute,
		135 * time.Minute,
		240 * time.Minute,
		375 * time.Minute,
		540 * time.Minute,
	}
	for i, w := range want {
		if got := s.at(i + 1).Sub(anchor); got != w {
			t.Errorf("pulse %d at +%s, want +%s", i+1, got, w)
		}
	}
	if !s.at(0).IsZero() {
		t.Error("pulse 0 is not a pulse; the roster is 1-based")
	}
}

// latest() and at() are two views of one roster and must never disagree — the
// scan path uses the first and the status roster uses the second.
func TestPulseScheduleLatestAgreesWithAt(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := newPulseSchedule(anchor)

	if _, _, ok := s.latest(anchor.Add(hbQuietMin - time.Minute)); ok {
		t.Error("a pulse was reported due before the quiet threshold")
	}
	for idx := 1; idx <= 8; idx++ {
		// One second into the pulse: still that pulse, never the next.
		p, next, ok := s.latest(s.at(idx).Add(time.Second))
		if !ok {
			t.Fatalf("pulse %d not due at its own trigger time", idx)
		}
		if p.Index != idx || !p.At.Equal(s.at(idx)) {
			t.Errorf("at pulse %d: latest = p%d %s, want p%d %s",
				idx, p.Index, p.At.Format("15:04"), idx, s.at(idx).Format("15:04"))
		}
		if got := p.At.Add(next); !got.Equal(s.at(idx + 1)) {
			t.Errorf("pulse %d + next interval = %s, want pulse %d at %s",
				idx, got.Format("15:04"), idx+1, s.at(idx+1).Format("15:04"))
		}
		// One second before the next one: must still be this pulse.
		if p2, _, _ := s.latest(s.at(idx + 1).Add(-time.Second)); p2.Index != idx {
			t.Errorf("one second before pulse %d: latest = p%d, want p%d", idx+1, p2.Index, idx)
		}
	}
}

func TestUpcomingIsStrictlyAheadAndBounded(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := newPulseSchedule(anchor)
	now := s.at(3).Add(time.Minute)

	got := s.upcoming(now, 3)
	if len(got) != 3 {
		t.Fatalf("upcoming returned %d pulses, want 3", len(got))
	}
	for i, p := range got {
		if !p.At.After(now) {
			t.Errorf("upcoming[%d] at %s is not after now", i, p.At.Format("15:04"))
		}
		if p.Index != 4+i {
			t.Errorf("upcoming[%d] index = %d, want %d", i, p.Index, 4+i)
		}
	}

	// Past the activity window the session is not pulsed at all, so the roster
	// must stop rather than advertise triggers that can never fire.
	for _, p := range s.upcoming(anchor, 100) {
		if p.At.Sub(anchor) > hbActivityWindow {
			t.Fatalf("roster offered p%d at +%s, past the %s activity window",
				p.Index, p.At.Sub(anchor), hbActivityWindow)
		}
	}
}

// simulateQuietPeriod walks one quiet period the way the scheduler does —
// select, record, advance — and returns the tasks that ran, in order. Now is
// taken as the pulse's own trigger time, i.e. a daemon that is awake and
// scanning promptly.
func simulateQuietPeriod(lastActive time.Time, quiet time.Duration, loc *time.Location) []string {
	sched := newPulseSchedule(lastActive)
	var fired []firedRecord
	var ran []string
	for idx := 1; idx <= 40; idx++ {
		at := sched.at(idx)
		if at.Sub(lastActive) > quiet || at.Sub(lastActive) > hbActivityWindow {
			break
		}
		task := selectHeartbeatTask(pulseState{
			SessionKey: "sim", Index: idx, Now: at, Trigger: at,
			LastActive: lastActive, Elapsed: at.Sub(lastActive), Fired: fired, Loc: loc,
		})
		if task == "" {
			continue
		}
		fired = append(fired, firedRecord{Task: task, At: at, Pulse: idx, Epoch: lastActive})
		ran = append(ran, task)
	}
	return ran
}

// The regression this whole roster exists for.
//
// Reflect used to be `pulse_index == 4`, so whenever pulse 4 landed inside the
// 02:00–06:00 dream window the dream took it and that quiet period's reflect was
// destroyed — no retry, because the index passes 4 exactly once and resets when
// the user speaks. Replayed over this deployment's entire history that was 52 of
// 391 reflect opportunities, 13.3%.
//
// Pulse 4 is exactly +4h, so the collision is not an edge case: it happens for
// every last-message time between 22:00 and 02:00, which is when conversations
// most often end. This sweeps all 1440 possible minutes and asserts the property
// the old code could not hold — a long enough silence always gets its reflect,
// no matter what hour it starts at.
func TestEveryLongQuietPeriodGetsExactlyOneReflect(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 5, 31, 0, 0, 0, 0, loc)

	for minute := range 1440 {
		lastActive := day.Add(time.Duration(minute) * time.Minute)
		// 8h reaches pulse 5, so a reflect displaced at pulse 4 still has a pulse
		// left to land on.
		ran := simulateQuietPeriod(lastActive, 8*time.Hour, loc)

		reflects, dreams := 0, 0
		for _, task := range ran {
			switch task {
			case hbTaskReflect:
				reflects++
			case hbTaskDream:
				dreams++
			}
		}
		if reflects != 1 {
			t.Fatalf("last message %s: reflect ran %d times, want exactly 1 (tasks: %v)",
				lastActive.Format("15:04"), reflects, ran)
		}
		if dreams > 1 {
			t.Fatalf("last message %s: dream ran %d times in one night (tasks: %v)",
				lastActive.Format("15:04"), dreams, ran)
		}
	}
}

// The collision the sweep above is really about, named explicitly so a failure
// says which case broke: an evening conversation ending between 22:00 and 02:00
// puts pulse 4 inside the dream window every time.
func TestEveningConversationsStillReflectDespiteTheDream(t *testing.T) {
	loc := time.UTC
	for hour := 22; hour < 26; hour++ {
		lastActive := time.Date(2026, 5, 31, hour%24, 0, 0, 0, loc)
		if hour >= 24 {
			lastActive = lastActive.AddDate(0, 0, 1)
		}
		ran := simulateQuietPeriod(lastActive, 8*time.Hour, loc)

		var sawDream, sawReflect bool
		for _, task := range ran {
			sawDream = sawDream || task == hbTaskDream
			sawReflect = sawReflect || task == hbTaskReflect
		}
		if !sawDream {
			t.Errorf("last message %02d:00: no dream, but pulse 4 lands at %02d:00",
				hour%24, (hour+4)%24)
		}
		if !sawReflect {
			t.Errorf("last message %02d:00: dream took the pulse and the reflect was lost (tasks: %v)",
				hour%24, ran)
		}
	}
}

// `heartbeat status` predicts forward, threading each predicted task into the
// set the next pulse is judged against. Without that, an owed reflect shows up
// on every remaining row and the roster reads as "reflect runs four times".
func TestStatusRosterPredictsForward(t *testing.T) {
	lastActive := time.Date(2026, 5, 31, 9, 0, 0, 0, time.Local) // daytime start
	s := &heartbeatScheduler{
		sessions: map[string]*hbSessionState{},
		cfgFn:    func() *config.Config { return &config.Config{} },
	}
	// Ask from just before pulse 4, so the roster covers p4..p7.
	now := newPulseSchedule(lastActive).at(4).Add(-time.Minute)

	roster := s.rosterFor("test:roster", now, lastActive)
	if len(roster) != hbStatusRosterLen {
		t.Fatalf("roster = %v, want %d rows", roster, hbStatusRosterLen)
	}
	reflects := 0
	for _, row := range roster {
		if strings.HasSuffix(row, " "+hbTaskReflect) {
			reflects++
		}
	}
	if reflects != 1 {
		t.Errorf("roster promises %d reflects, want exactly 1: %v", reflects, roster)
	}
	if !strings.HasSuffix(roster[0], " "+hbTaskReflect) {
		t.Errorf("the reflect should land on the first eligible pulse: %v", roster)
	}
}

// A quiet period too short to reach the reflect floor must not produce one — the
// floor is a floor, not a "run it eventually".
func TestShortQuietPeriodsRunNoReflect(t *testing.T) {
	loc := time.UTC
	lastActive := time.Date(2026, 5, 31, 12, 0, 0, 0, loc) // daytime: no dream competes
	for _, task := range simulateQuietPeriod(lastActive, 3*time.Hour, loc) {
		t.Errorf("a 3h quiet period ran %q; pulse 4 is at +4h", task)
	}
}
