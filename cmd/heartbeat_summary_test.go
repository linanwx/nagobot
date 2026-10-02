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
	const summary = "AI news and fact-checking with Nansen"
	now := time.Now()

	dreaming := buildHeartbeatMessage("", "", 3, time.Hour, now, hbTaskDream, summary)
	if !strings.Contains(dreaming, "task: dream") {
		t.Fatalf("dream wake lost its routing key:\n%s", dreaming)
	}
	if !strings.Contains(dreaming, summary) {
		t.Errorf("dream wake does not carry the summary:\n%s", dreaming)
	}

	// No summary on record: the field must be PRESENT and say so.
	blank := buildHeartbeatMessage("", "", 3, time.Hour, now, hbTaskDream, "")
	if !strings.Contains(blank, "session_summary") {
		t.Errorf("a session with no summary must still carry the field:\n%s", blank)
	}
	if !strings.Contains(blank, noSessionSummary) {
		t.Errorf("missing summary is not stated explicitly:\n%s", blank)
	}

	// Any other task judges no summary — no field, and no wasted read behind it.
	reflecting := buildHeartbeatMessage("", "", 4, time.Hour, now, hbTaskReflect, summary)
	if strings.Contains(reflecting, "session_summary") {
		t.Errorf("a reflect pulse carries session_summary:\n%s", reflecting)
	}
	if !strings.Contains(reflecting, "task: reflect") {
		t.Errorf("reflect wake lost its routing key:\n%s", reflecting)
	}
}

// A wake with no selected task must not leave a stale task behind.
func TestHeartbeatWakeOmitsTaskWhenNoneWasSelected(t *testing.T) {
	msg := buildHeartbeatMessage("", "", 2, time.Hour, time.Now(), "", "")
	if strings.Contains(msg, "task:") {
		t.Errorf("a taskless pulse still carries a task field:\n%s", msg)
	}
}

func TestHeartbeatWakeCallsSelectedSkillDirectly(t *testing.T) {
	for _, tc := range []struct {
		task string
		want string
	}{
		{hbTaskDream, `use_skill("dream")`},
		{hbTaskReflect, `use_skill("session-reflect")`},
		{"", "Call dispatch({})"},
		{"unknown", "Call dispatch({})"},
	} {
		t.Run(tc.task, func(t *testing.T) {
			msg := buildHeartbeatMessage("", "", 3, time.Hour, time.Now(), tc.task, "")
			if !strings.Contains(msg, tc.want) {
				t.Errorf("wake must instruct %s:\n%s", tc.want, msg)
			}
			if strings.Contains(msg, "heartbeat-wake") {
				t.Errorf("wake still loads the routing skill:\n%s", msg)
			}
			if (tc.task == "" || tc.task == "unknown") && strings.Contains(msg, "use_skill(") {
				t.Errorf("unrecognized task must not load a skill:\n%s", msg)
			}
		})
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
