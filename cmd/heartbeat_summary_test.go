package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The dream decides whether to rewrite the session summary, so the summary it
// judges has to be in the wake. Three properties matter, and the third is the
// one that fails silently: an ABSENT field reads as "not applicable" and the
// dream skips step 4 — exactly backwards for a session that has never had a
// summary written.
func TestHeartbeatWakeCarriesSessionSummaryOnlyWhenDreaming(t *testing.T) {
	const summary = "Nansen main session. Fact-checking AI news."
	now := time.Now()

	dreaming := buildHeartbeatMessage("", "", 3, time.Hour, now, true, summary)
	if !strings.Contains(dreaming, "should_dream") {
		t.Fatalf("dream wake lost should_dream:\n%s", dreaming)
	}
	if !strings.Contains(dreaming, summary) {
		t.Errorf("dream wake does not carry the summary:\n%s", dreaming)
	}

	// No summary on record: the field must be PRESENT and say so.
	blank := buildHeartbeatMessage("", "", 3, time.Hour, now, true, "")
	if !strings.Contains(blank, "session_summary") {
		t.Errorf("a session with no summary must still carry the field:\n%s", blank)
	}
	if !strings.Contains(blank, noSessionSummary) {
		t.Errorf("missing summary is not stated explicitly:\n%s", blank)
	}

	// An ordinary pulse wakes nothing that judges the summary — no field, and
	// no wasted read behind it.
	ordinary := buildHeartbeatMessage("", "", 2, time.Hour, now, false, summary)
	if strings.Contains(ordinary, "session_summary") {
		t.Errorf("non-dream pulse carries session_summary:\n%s", ordinary)
	}
}

// The summary is one YAML scalar in the wake frontmatter, and summaries are
// free prose — a newline in one would split the block.
func TestSessionSummaryIsCollapsedToOneLine(t *testing.T) {
	// Built directly rather than via newHeartbeatScheduler: the constructor
	// calls cfgFn, and sessionSummary needs nothing but the path.
	s := &heartbeatScheduler{
		summaryPath: writeSummaryFixture(t, `{"cli":{"summary":"first line\nsecond line"}}`),
	}
	if got := s.sessionSummary("cli"); got != "first line second line" {
		t.Errorf("sessionSummary = %q, want the newline collapsed", got)
	}
	if got := s.sessionSummary("nope"); got != "" {
		t.Errorf("unknown session = %q, want empty (caller renders the marker)", got)
	}
}

func writeSummaryFixture(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sessions_summary.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
