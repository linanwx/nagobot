package cmd

import "time"

// Task names. These are the values the wake payload carries and the
// heartbeat-wake skill routes on, so they are the contract with the skill.
const (
	hbTaskDream   = "dream"
	hbTaskReflect = "reflect"
)

// firedRecord is one heartbeat task execution. Persisted with the session's
// heartbeat state so the dedup rules below survive a restart: a deploy at 03:00
// must not re-run a dream that already happened at 02:10.
type firedRecord struct {
	Task  string    `json:"task"`
	At    time.Time `json:"at"`
	Pulse int       `json:"pulse"`
	Epoch time.Time `json:"epoch"` // the lastActive this pulse was measured from
}

// pulseState is everything a task predicate is allowed to look at. It carries
// two independent clocks on purpose: Index answers "how long has the user been
// gone", in pulses, while Now answers "what time is it where they live". dream
// reads the second, reflect the first, and a future task may read either.
//
// Trigger is the moment this pulse was scheduled for, which can be earlier than
// Now when the daemon was busy or down. No predicate uses it today — dream
// deliberately tests Now, so that a catch-up after a nighttime outage cannot run
// a memory-rewriting turn in broad daylight.
type pulseState struct {
	SessionKey string
	Index      int
	Now        time.Time
	Trigger    time.Time
	LastActive time.Time
	Elapsed    time.Duration
	Fired      []firedRecord
	Loc        *time.Location
}

// firedWithin reports whether task ran in the d before Now, ACROSS quiet
// periods — the right question for anything on a wall-clock cycle.
func (p pulseState) firedWithin(task string, d time.Duration) bool {
	for _, f := range p.Fired {
		if f.Task != task || f.At.After(p.Now) {
			continue
		}
		if p.Now.Sub(f.At) < d {
			return true
		}
	}
	return false
}

// firedThisEpoch reports whether task already ran during THIS quiet period. The
// epoch is identified by the anchor the roster was built from, so the next user
// message starts a fresh one.
func (p pulseState) firedThisEpoch(task string) bool {
	for _, f := range p.Fired {
		if f.Task == task && f.Epoch.Equal(p.LastActive) {
			return true
		}
	}
	return false
}

// nightHour reports whether t falls in the 02:00–06:00 window of loc.
func nightHour(t time.Time, loc *time.Location) bool {
	if loc == nil {
		loc = time.Local
	}
	h := t.In(loc).Hour()
	return h >= 2 && h < 6
}

// heartbeatTask is one thing the heartbeat can decide to do when a pulse comes
// due. Eligible must be pure: it is called on every due pulse for every session
// and may not touch IO.
type heartbeatTask struct {
	Name     string
	Eligible func(pulseState) bool
}

// heartbeatTasks is the roster, and ITS ORDER IS THE PRIORITY. At most one task
// runs per pulse: the first eligible one wins, and the others stay eligible for
// a later pulse. That is not an optimization — it is the mechanism by which a
// reflect that loses its pulse to a dream comes back on the next one instead of
// being lost for the whole quiet period.
//
// Adding a condition means adding an entry here. Nothing else — not the scan
// loop, not the wake payload builder — needs to learn its name; only the
// heartbeat-wake skill's routing table, which reads the task name verbatim.
var heartbeatTasks = []heartbeatTask{
	{
		// Dream is scheduled by the CLOCK, not by the pulse count. It rewrites
		// memory summaries, the session summary and tracked work files, all of
		// which invalidate the cached prompt prefix — so it belongs in the dead
		// of night, when nothing is reading. Any pulse inside the window will do.
		//
		// The index floor is the one concession to the pulse count: without it a
		// message at 01:50 followed by fifteen minutes of silence would dream at
		// 02:05, busting the cache of a conversation the user may still be in.
		// Pulse 2 is one hour of quiet.
		Name: hbTaskDream,
		Eligible: func(p pulseState) bool {
			if p.Index < hbDreamMinPulse {
				return false
			}
			if !nightHour(p.Now, p.Loc) {
				return false
			}
			// Cross-epoch on purpose: a 48h silence spans two nights and should
			// dream on both, so this asks the wall clock rather than the quiet
			// period.
			return !p.firedWithin(hbTaskDream, hbDreamDedup)
		},
	},
	{
		// Reflect is scheduled by the pulse COUNT. It wants the conversation to
		// be genuinely over rather than paused, and by pulse 4 (+4h) the
		// provider's prompt cache has expired anyway, so the turn's real cost is
		// already sunk.
		//
		// A FLOOR, not an equality. It used to be `== hbReflectPulse`, which
		// meant a dream taking pulse 4 destroyed that quiet period's reflect
		// outright — measured across this deployment's whole history at 52 of
		// 391 reflect opportunities, 13.3%, with no catch-up of any kind because
		// the pulse index only passes 4 once and resets when the user speaks.
		// With a floor the displaced reflect simply runs at pulse 5. The
		// once-per-epoch guard is what keeps the floor from re-firing it on
		// every later pulse, so the volume of reflects is unchanged.
		Name: hbTaskReflect,
		Eligible: func(p pulseState) bool {
			if p.Index < hbReflectMinPulse {
				return false
			}
			return !p.firedThisEpoch(hbTaskReflect)
		},
	},
}

// selectHeartbeatTask returns the highest-priority eligible task, or "" when the
// pulse has nothing to do — which is most pulses, and costs no LLM call.
func selectHeartbeatTask(p pulseState) string {
	for _, t := range heartbeatTasks {
		if t.Eligible(p) {
			return t.Name
		}
	}
	return ""
}

// pruneFired drops records that have aged out of the activity window. Past it a
// session is not pulsed at all, so no predicate can still be asking about them.
func pruneFired(records []firedRecord, now time.Time) []firedRecord {
	out := records[:0]
	for _, f := range records {
		if now.Sub(f.At) <= hbActivityWindow {
			out = append(out, f)
		}
	}
	return out
}
