package tools

import (
	"testing"
	"time"
)

func resetBraveState() {
	braveRot.Store(0)
	braveCDMu.Lock()
	braveCD = map[string]time.Time{}
	braveCDMu.Unlock()
	braveNowFn = time.Now
}

func TestParseBraveKeys(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"k1", []string{"k1"}},
		{"k1,k2,k3", []string{"k1", "k2", "k3"}},
		{" k1 , k2 ,, k3 ", []string{"k1", "k2", "k3"}},
		{"k1\nk2\r\nk3", []string{"k1", "k2", "k3"}},
		{"k1,k2,k1", []string{"k1", "k2"}}, // de-dup, order preserved
	}
	for _, c := range cases {
		got := parseBraveKeys(c.in)
		if len(got) != len(c.want) {
			t.Errorf("parseBraveKeys(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parseBraveKeys(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

// TestBraveRoundRobin verifies consecutive calls start at the next key so load
// spreads evenly across the pool.
func TestBraveRoundRobin(t *testing.T) {
	resetBraveState()
	defer resetBraveState()
	keys := []string{"a", "b", "c"}
	firsts := make([]string, 3)
	for i := 0; i < 3; i++ {
		firsts[i] = braveOrderedKeys(keys)[0]
	}
	// Three calls should start at a, b, c respectively (rotation).
	if firsts[0] == firsts[1] || firsts[1] == firsts[2] || firsts[0] == firsts[2] {
		t.Errorf("expected rotation across 3 distinct starts, got %v", firsts)
	}
	if got := braveOrderedKeys(keys); len(got) != 3 {
		t.Fatalf("expected 3 keys, got %v", got)
	}
}

func TestBraveRoundRobin_SingleKey(t *testing.T) {
	resetBraveState()
	defer resetBraveState()
	got := braveOrderedKeys([]string{"only"})
	if len(got) != 1 || got[0] != "only" {
		t.Errorf("single key should pass through, got %v", got)
	}
}

func TestBraveCooldown(t *testing.T) {
	resetBraveState()
	defer resetBraveState()
	base := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	braveNowFn = func() time.Time { return base }

	if braveOnCooldown("k") {
		t.Fatal("fresh key should not be on cooldown")
	}
	braveMarkCooldown("k")
	if !braveOnCooldown("k") {
		t.Fatal("key should be on cooldown right after marking")
	}
	// Within cooldown window.
	braveNowFn = func() time.Time { return base.Add(braveCooldownDur - time.Second) }
	if !braveOnCooldown("k") {
		t.Fatal("key should still be on cooldown before window elapses")
	}
	// After cooldown window — auto-clears.
	braveNowFn = func() time.Time { return base.Add(braveCooldownDur + time.Second) }
	if braveOnCooldown("k") {
		t.Fatal("key should be live after cooldown window")
	}
}

func TestBraveExhausted(t *testing.T) {
	for _, s := range []int{429, 402, 403} {
		if !braveExhausted(s) {
			t.Errorf("status %d should be treated as exhausted (cooldown)", s)
		}
	}
	for _, s := range []int{200, 400, 401, 500} {
		if braveExhausted(s) {
			t.Errorf("status %d should NOT trigger cooldown", s)
		}
	}
}
