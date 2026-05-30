package cmd

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/linanwx/nagobot/config"
)

// newDreamTestScheduler builds a scheduler with only the fields the dream logic
// touches (cfgFn, sessions, lastDream, dreamLogPath). mgr is unused by
// shouldDream/recordDream/loadDreamLog.
func newDreamTestScheduler(t *testing.T) *heartbeatScheduler {
	t.Helper()
	return &heartbeatScheduler{
		cfgFn:        func() *config.Config { return &config.Config{} },
		sessions:     make(map[string]*hbSessionState),
		lastDream:    make(map[string]time.Time),
		dreamLogPath: filepath.Join(t.TempDir(), "dream_log.jsonl"),
	}
}

func TestShouldDream_Conditions(t *testing.T) {
	s := newDreamTestScheduler(t)
	key := "test:dream-conditions" // unique key → SessionTimezone falls back to local tz

	// Anchor times to the local zone so the 02:00–06:00 night check is
	// deterministic regardless of the machine/CI timezone.
	night := time.Date(2026, 5, 31, 3, 0, 0, 0, time.Local)  // 03:00 local
	noon := time.Date(2026, 5, 31, 12, 0, 0, 0, time.Local)  // 12:00 local

	if s.shouldDream(key, night, 2) {
		t.Error("pulse_index == 2 must not dream (only pulse_index > 2)")
	}
	if s.shouldDream(key, night, 1) {
		t.Error("pulse_index == 1 must not dream")
	}
	if s.shouldDream(key, noon, 3) {
		t.Error("daytime (12:00) must not dream even with pulse_index > 2")
	}
	if !s.shouldDream(key, night, 3) {
		t.Fatal("night (03:00) + pulse_index > 2 + no prior dream should dream")
	}
}

func TestShouldDream_DedupWindow(t *testing.T) {
	s := newDreamTestScheduler(t)
	key := "test:dream-dedup"

	night := time.Date(2026, 5, 31, 3, 0, 0, 0, time.Local)
	if !s.shouldDream(key, night, 3) {
		t.Fatal("first night pulse should dream")
	}

	// Simulate the scheduler firing the dream.
	s.recordDream(key, night)

	// 2h later, still night, still within the 4h dedup window → no dream.
	if s.shouldDream(key, night.Add(2*time.Hour), 4) {
		t.Error("within 4h dedup window must not dream again")
	}

	// Next night, well past the dedup window → dream again.
	nextNight := time.Date(2026, 6, 1, 4, 0, 0, 0, time.Local)
	if !s.shouldDream(key, nextNight, 3) {
		t.Error("past the dedup window on the next night should dream again")
	}
}

// TestDreamDedup_PersistsAcrossReload covers requirement #4: a nighttime
// restart must not re-trigger a dream already done, because the dedup state is
// persisted to dream_log.jsonl and rebuilt on load.
func TestDreamDedup_PersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "dream_log.jsonl")
	key := "test:dream-persist"
	night := time.Date(2026, 5, 31, 3, 0, 0, 0, time.Local)

	// First scheduler instance fires + records a dream.
	s1 := &heartbeatScheduler{
		cfgFn:        func() *config.Config { return &config.Config{} },
		sessions:     make(map[string]*hbSessionState),
		lastDream:    make(map[string]time.Time),
		dreamLogPath: logPath,
	}
	s1.recordDream(key, night)

	// Simulate a restart: a fresh scheduler loads the persisted log.
	s2 := &heartbeatScheduler{
		cfgFn:        func() *config.Config { return &config.Config{} },
		sessions:     make(map[string]*hbSessionState),
		lastDream:    make(map[string]time.Time),
		dreamLogPath: logPath,
	}
	s2.loadDreamLog()

	if _, ok := s2.lastDream[key]; !ok {
		t.Fatal("loadDreamLog should rebuild the last-dream timestamp after restart")
	}
	// Same night, within dedup window after restart → must not re-dream.
	if s2.shouldDream(key, night.Add(time.Hour), 3) {
		t.Error("after restart, a session that already dreamed tonight must not re-dream")
	}
}
