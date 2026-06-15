package thread

import (
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/thread/msg"
)

func TestUserFacingAncestor(t *testing.T) {
	cases := []struct {
		key     string
		wantAnc string
		wantOK  bool
	}{
		{"telegram:123:threads:find-x", "telegram:123", true},
		{"cli:fork:hypo-a", "cli", true},
		{"telegram:123:threads:a:threads:b", "telegram:123", true}, // nested → topmost
		{"cli:fork:x:threads:y", "cli", true},                      // earliest infix wins
		{"telegram:123", "telegram:123", true},                     // not a child; key itself is user-facing (child-ness checked in progressEligible)
		{"cli", "cli", true},                                       // not a child; key itself is user-facing
		{"cron:job1:threads:t", "cron:job1", false},                // ancestor not user-facing
		{"heartbeat:threads:t", "heartbeat", false},                // ancestor not user-facing
	}
	for _, c := range cases {
		anc, ok := userFacingAncestor(c.key)
		if anc != c.wantAnc || ok != c.wantOK {
			t.Errorf("userFacingAncestor(%q) = (%q, %v), want (%q, %v)", c.key, anc, ok, c.wantAnc, c.wantOK)
		}
	}
}

func TestProgressEligible(t *testing.T) {
	base := msg.ThreadInfo{SessionKey: "telegram:1:threads:x", State: "running", ElapsedSec: 120}

	if anc, ok := progressEligible(base); !ok || anc != "telegram:1" {
		t.Errorf("eligible child not accepted: anc=%q ok=%v", anc, ok)
	}

	notRunning := base
	notRunning.State = "idle"
	if _, ok := progressEligible(notRunning); ok {
		t.Error("idle thread should not be eligible")
	}

	tooYoung := base
	tooYoung.ElapsedSec = progressMinElapsed - 1
	if _, ok := progressEligible(tooYoung); ok {
		t.Error("thread under min elapsed should not be eligible")
	}

	notChild := msg.ThreadInfo{SessionKey: "telegram:1", State: "running", ElapsedSec: 120}
	if _, ok := progressEligible(notChild); ok {
		t.Error("non-child session should not be eligible")
	}

	systemParent := msg.ThreadInfo{SessionKey: "cron:job:threads:x", State: "running", ElapsedSec: 120}
	if _, ok := progressEligible(systemParent); ok {
		t.Error("child of non-user-facing parent should not be eligible")
	}
}

func TestFormatProgress(t *testing.T) {
	info := msg.ThreadInfo{
		SessionKey:     "cli:threads:find-x",
		ElapsedSec:     372,
		TotalToolCalls: 124,
		CurrentTool:    "grep",
		ToolTrace: []msg.ToolCallRecord{
			{Name: "old1"}, {Name: "old2"}, {Name: "old3"}, // should be dropped (window=3)
			{Name: "web_search", ArgsSummary: `{"q":"x"}`},
			{Name: "fetch", ArgsSummary: `{"url":"y"}`, Error: true},
			{Name: "read_file", ArgsSummary: `{"path":"z"}`},
		},
	}
	out := formatProgress(info.SessionKey, info)

	if !strings.Contains(out, "cli:threads:find-x") {
		t.Error("progress must include the child key (for stop-session)")
	}
	if !strings.Contains(out, "6m12s") {
		t.Errorf("expected humanized duration 6m12s, got: %s", out)
	}
	if !strings.Contains(out, "124 步") {
		t.Error("expected step count")
	}
	// Only the last 3 tool calls appear.
	if strings.Contains(out, "old1") || strings.Contains(out, "old2") || strings.Contains(out, "old3") {
		t.Errorf("window not applied, old calls present: %s", out)
	}
	if !strings.Contains(out, "web_search") || !strings.Contains(out, "fetch") || !strings.Contains(out, "read_file") {
		t.Errorf("recent tool calls missing: %s", out)
	}
	if !strings.Contains(out, "✗") {
		t.Error("error marker missing for failed call")
	}
	if !strings.Contains(out, "正在执行 grep") {
		t.Error("current tool line missing")
	}
}

func TestHumanizeDuration(t *testing.T) {
	cases := map[int]string{
		45:   "45s",
		60:   "1m00s",
		372:  "6m12s",
		3600: "1h00m",
		3725: "1h02m",
	}
	for sec, want := range cases {
		if got := humanizeDuration(sec); got != want {
			t.Errorf("humanizeDuration(%d) = %q, want %q", sec, got, want)
		}
	}
}

func TestCapBytes_RuneSafe(t *testing.T) {
	// 10 Chinese chars = 30 bytes. Cap at 16 bytes → must not split a rune.
	s := strings.Repeat("中", 10)
	out := capBytes(s, 16)
	if !utf8ValidString(out) {
		t.Errorf("capBytes produced invalid UTF-8: %q", out)
	}
	if len([]byte(out)) > 16+len("…") {
		t.Errorf("capBytes exceeded cap: %d bytes", len([]byte(out)))
	}
	// Short string passes through unchanged.
	if capBytes("hi", 100) != "hi" {
		t.Error("short string should be unchanged")
	}
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestScanOnce_ThrottleAndPrune(t *testing.T) {
	m := NewManager(nil)

	child := &Thread{
		id:         "thread-child",
		sessionKey: "telegram:99:threads:find-x",
		state:      threadRunning,
		inbox:      make(chan *WakeMessage, 8),
		signal:     m.signal,
		execMetrics: &ExecMetrics{
			TurnStart:      time.Now().Add(-2 * time.Minute),
			TotalToolCalls: 3,
			ToolCalls:      []msg.ToolCallRecord{{Name: "web_search"}},
		},
	}
	ancestor := &Thread{
		id:         "thread-anc",
		sessionKey: "telegram:99",
		state:      threadIdle,
		inbox:      make(chan *WakeMessage, 8),
		signal:     m.signal,
	}
	m.threads[child.sessionKey] = child
	m.threads[ancestor.sessionKey] = ancestor

	ps := NewProgressScanner(m)

	ps.scanOnce()
	if got := len(ancestor.inbox); got != 1 {
		t.Fatalf("expected 1 progress wake delivered to ancestor, got %d", got)
	}
	if _, ok := ps.lastReport[child.sessionKey]; !ok {
		t.Fatal("lastReport not recorded for child")
	}

	// Immediate second scan → throttled, no new wake.
	ps.scanOnce()
	if got := len(ancestor.inbox); got != 1 {
		t.Fatalf("throttle failed: expected still 1 wake, got %d", got)
	}

	// Child finishes → state pruned.
	delete(m.threads, child.sessionKey)
	ps.scanOnce()
	if _, ok := ps.lastReport[child.sessionKey]; ok {
		t.Error("lastReport not pruned after child stopped")
	}
}
