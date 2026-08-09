package cmd

import (
	"testing"
	"time"
)

// dreamState builds the pulse a dream predicate would see. Times are anchored to
// the local zone so the 02:00–06:00 check is deterministic on any CI machine.
func dreamState(now time.Time, index int, fired ...firedRecord) pulseState {
	return pulseState{
		SessionKey: "test:dream",
		Index:      index,
		Now:        now,
		Trigger:    now,
		LastActive: now.Add(-time.Duration(index) * time.Hour),
		Fired:      fired,
		Loc:        time.Local,
	}
}

func eligible(t *testing.T, name string, p pulseState) bool {
	t.Helper()
	for _, task := range heartbeatTasks {
		if task.Name == name {
			return task.Eligible(p)
		}
	}
	t.Fatalf("no task named %q in the roster", name)
	return false
}

func TestDreamEligibility_Conditions(t *testing.T) {
	night := time.Date(2026, 5, 31, 3, 0, 0, 0, time.Local) // 03:00 local
	noon := time.Date(2026, 5, 31, 12, 0, 0, 0, time.Local) // 12:00 local

	if eligible(t, hbTaskDream, dreamState(night, 1)) {
		t.Error("pulse 1 must not dream — 15 minutes of quiet is not a night's rest")
	}
	if eligible(t, hbTaskDream, dreamState(noon, 3)) {
		t.Error("daytime (12:00) must not dream at any pulse index")
	}
	if !eligible(t, hbTaskDream, dreamState(night, hbDreamMinPulse)) {
		t.Fatalf("pulse %d at 03:00 with no prior dream should dream", hbDreamMinPulse)
	}
	// The window is what schedules a dream, not the pulse count: once the floor
	// is cleared, every later pulse inside the window is equally good.
	if !eligible(t, hbTaskDream, dreamState(night, 9)) {
		t.Error("a late pulse inside the night window should still dream")
	}
}

// The dedup is a wall-clock window, not a per-quiet-period flag, so a 48h
// silence dreams on both nights it spans.
func TestDreamDedupIsWallClockNotPerEpoch(t *testing.T) {
	night := time.Date(2026, 5, 31, 3, 0, 0, 0, time.Local)
	ran := firedRecord{Task: hbTaskDream, At: night, Pulse: 3, Epoch: night.Add(-3 * time.Hour)}

	if eligible(t, hbTaskDream, dreamState(night.Add(2*time.Hour), 4, ran)) {
		t.Error("within the 4h dedup window must not dream again")
	}

	// Next night, same uninterrupted silence (so the same epoch), well past the
	// window. A per-epoch guard would wrongly suppress this one.
	nextNight := time.Date(2026, 6, 1, 4, 0, 0, 0, time.Local)
	p := dreamState(nextNight, 9, ran)
	p.LastActive = ran.Epoch
	if !eligible(t, hbTaskDream, p) {
		t.Error("a 48h silence must dream on the second night too")
	}
}

// A record from the future (clock skew, a hand-edited state file) must not be
// read as "already dreamed" forever.
func TestDreamDedupIgnoresFutureRecords(t *testing.T) {
	night := time.Date(2026, 5, 31, 3, 0, 0, 0, time.Local)
	future := firedRecord{Task: hbTaskDream, At: night.Add(time.Hour), Pulse: 3, Epoch: night}
	if !eligible(t, hbTaskDream, dreamState(night, 3, future)) {
		t.Error("a fired record dated in the future must not suppress the current pulse")
	}
}

// Reflect used to be `pulse_index == 4`, so a dream landing on pulse 4 destroyed
// that quiet period's reflect outright — 13.3% of all reflect opportunities on
// the live deployment, with no catch-up because the index passes 4 exactly once.
// The floor plus the per-epoch guard is what turns that loss into a delay.
func TestReflectDisplacedByDreamRunsOnTheNextPulse(t *testing.T) {
	// 22:00 last message → pulse 4 lands at 02:00, inside the night window.
	lastActive := time.Date(2026, 5, 30, 22, 0, 0, 0, time.Local)
	sched := newPulseSchedule(lastActive)

	base := func(index int, fired []firedRecord) pulseState {
		return pulseState{
			SessionKey: "test:displaced",
			Index:      index,
			Now:        sched.at(index),
			Trigger:    sched.at(index),
			LastActive: lastActive,
			Fired:      fired,
			Loc:        time.Local,
		}
	}

	p4 := base(4, nil)
	if got := nightHour(p4.Now, time.Local); !got {
		t.Fatalf("fixture broken: pulse 4 at %s is not in the night window", p4.Now.Format("15:04"))
	}
	if got := selectHeartbeatTask(p4); got != hbTaskDream {
		t.Fatalf("pulse 4 selected %q, want dream to win on priority", got)
	}

	// The dream ran; reflect has not. The next pulse must pick it up.
	fired := []firedRecord{{Task: hbTaskDream, At: p4.Now, Pulse: 4, Epoch: lastActive}}
	if got := selectHeartbeatTask(base(5, fired)); got != hbTaskReflect {
		t.Fatalf("pulse 5 selected %q, want the displaced reflect to run", got)
	}
}

// The floor must not turn reflect into a per-pulse job: once it has run for this
// quiet period, every later pulse leaves it alone.
func TestReflectRunsOncePerQuietPeriod(t *testing.T) {
	lastActive := time.Date(2026, 5, 30, 9, 0, 0, 0, time.Local) // daytime: no dream competes
	sched := newPulseSchedule(lastActive)
	fired := []firedRecord{{Task: hbTaskReflect, At: sched.at(4), Pulse: 4, Epoch: lastActive}}

	for _, idx := range []int{4, 5, 6, 7} {
		p := pulseState{
			SessionKey: "test:once", Index: idx,
			Now: sched.at(idx), Trigger: sched.at(idx),
			LastActive: lastActive, Fired: fired, Loc: time.Local,
		}
		if got := selectHeartbeatTask(p); got != "" {
			t.Errorf("pulse %d selected %q after reflect already ran this quiet period", idx, got)
		}
	}

	// A new user message starts a new epoch, and reflect is owed again.
	next := lastActive.Add(8 * time.Hour)
	p := pulseState{
		SessionKey: "test:once", Index: 4,
		Now: newPulseSchedule(next).at(4), Trigger: newPulseSchedule(next).at(4),
		LastActive: next, Fired: fired, Loc: time.Local,
	}
	if got := selectHeartbeatTask(p); got != hbTaskReflect {
		t.Errorf("new quiet period selected %q, want reflect", got)
	}
}

// Below both floors nothing runs, which is what keeps the overwhelming majority
// of pulses free of any LLM call.
func TestMostPulsesSelectNothing(t *testing.T) {
	noon := time.Date(2026, 5, 31, 12, 0, 0, 0, time.Local)
	for _, idx := range []int{1, 2, 3} {
		p := pulseState{SessionKey: "k", Index: idx, Now: noon, Trigger: noon,
			LastActive: noon.Add(-2 * time.Hour), Loc: time.Local}
		if got := selectHeartbeatTask(p); got != "" {
			t.Errorf("daytime pulse %d selected %q, want nothing", idx, got)
		}
	}
}

// The state a restart reloads is what the predicates read, so a dream recorded
// before the restart must still suppress one after it.
func TestFiredRecordsSurviveAStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	night := time.Date(2026, 5, 31, 3, 0, 0, 0, time.Local)
	key := "test:persist"

	s1 := &heartbeatScheduler{
		sessions:  map[string]*hbSessionState{},
		statePath: dir + "/heartbeat-state.json",
	}
	s1.recordFired(key, hbTaskDream, night, 3, night.Add(-3*time.Hour))
	s1.saveState()

	s2 := &heartbeatScheduler{
		sessions:  map[string]*hbSessionState{},
		statePath: s1.statePath,
	}
	s2.loadState()

	st := s2.sessions[key]
	if st == nil || len(st.Fired) != 1 {
		t.Fatalf("reloaded state = %+v, want one fired record", st)
	}
	p := dreamState(night.Add(time.Hour), 4, st.Fired...)
	if eligible(t, hbTaskDream, p) {
		t.Error("after a restart, a session that already dreamed tonight must not re-dream")
	}
}

// Records age out with the activity window — past it the session is not pulsed
// at all, so nothing can still be asking about them.
func TestPruneFiredDropsRecordsPastTheActivityWindow(t *testing.T) {
	now := time.Date(2026, 5, 31, 3, 0, 0, 0, time.Local)
	in := []firedRecord{
		{Task: hbTaskDream, At: now.Add(-hbActivityWindow - time.Hour)},
		{Task: hbTaskReflect, At: now.Add(-time.Hour)},
	}
	got := pruneFired(in, now)
	if len(got) != 1 || got[0].Task != hbTaskReflect {
		t.Fatalf("pruneFired = %+v, want only the recent reflect", got)
	}
}
