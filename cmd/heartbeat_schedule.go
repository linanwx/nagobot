package cmd

import "time"

// scheduledPulse is one moment on the roster.
type scheduledPulse struct {
	Index int
	At    time.Time
}

// pulseSchedule is the complete roster of heartbeat trigger moments implied by a
// single user message. Every future trigger for that quiet period is fixed the
// instant the user speaks, so the roster is a pure function of that anchor.
//
// It is deliberately NOT persisted. The anchor moves on every user message,
// which invalidates the whole roster at once; a stored copy would be a second
// source of truth that drifts silently — the same trap Thread.lastUserActiveAt
// already is. Deriving costs a handful of additions and cannot disagree with the
// anchor it came from.
type pulseSchedule struct{ anchor time.Time }

func newPulseSchedule(lastActive time.Time) pulseSchedule {
	return pulseSchedule{anchor: lastActive}
}

// at returns the moment pulse i fires. i is 1-based: pulse 1 is the first one
// after the quiet threshold. The first offset is hbQuietMin and the gaps then
// grow by hbPulseGrowth, so pulses land at +15m, +60m, +135m, +240m, +375m, ...
func (s pulseSchedule) at(i int) time.Time {
	if i < 1 {
		return time.Time{}
	}
	t := s.anchor.Add(hbQuietMin)
	interval := hbPulseInterval
	for n := 1; n < i; n++ {
		t = t.Add(interval)
		interval += hbPulseGrowth
	}
	return t
}

// latest returns the last pulse due at or before now, plus the gap to the one
// after it. ok is false while the first pulse is still in the future.
func (s pulseSchedule) latest(now time.Time) (p scheduledPulse, next time.Duration, ok bool) {
	t := s.anchor.Add(hbQuietMin)
	if now.Before(t) {
		return scheduledPulse{}, 0, false
	}
	idx, interval := 1, hbPulseInterval
	for {
		n := t.Add(interval)
		if now.Before(n) {
			return scheduledPulse{Index: idx, At: t}, interval, true
		}
		t, idx = n, idx+1
		interval += hbPulseGrowth
	}
}

// upcoming returns the next n pulses strictly after now, stopping at the
// activity window — past it the session is no longer pulsed at all, so a roster
// that ran on would be advertising triggers that can never fire.
func (s pulseSchedule) upcoming(now time.Time, n int) []scheduledPulse {
	if n <= 0 {
		return nil
	}
	out := make([]scheduledPulse, 0, n)
	t, idx, interval := s.anchor.Add(hbQuietMin), 1, hbPulseInterval
	for len(out) < n && t.Sub(s.anchor) <= hbActivityWindow {
		if t.After(now) {
			out = append(out, scheduledPulse{Index: idx, At: t})
		}
		t = t.Add(interval)
		interval += hbPulseGrowth
		idx++
	}
	return out
}

// latestDueTrigger is the flat entry point the scan loop and its scenario tests
// are written against.
func latestDueTrigger(lastActive time.Time, now time.Time) (time.Time, time.Duration, int) {
	p, next, ok := newPulseSchedule(lastActive).latest(now)
	if !ok {
		return time.Time{}, 0, 0
	}
	return p.At, next, p.Index
}
