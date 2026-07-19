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
		{"telegram:123", "telegram:123", true},                     // not a child; key itself is user-facing (source gating in progressEligible)
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
	child := msg.ThreadInfo{
		SessionKey:     "telegram:1:threads:x",
		State:          "running",
		ElapsedSec:     120,
		TotalToolCalls: 3,
		TurnWakeSource: string(msg.WakeSession),
	}
	if target, ok := progressEligible(child); !ok || target != "telegram:1" {
		t.Errorf("eligible child not accepted: target=%q ok=%v", target, ok)
	}

	resumedChild := child
	resumedChild.TurnWakeSource = string(msg.WakeResume)
	if _, ok := progressEligible(resumedChild); !ok {
		t.Error("resumed child should be eligible")
	}

	compressionChild := child
	compressionChild.TurnWakeSource = string(msg.WakeCompression)
	if _, ok := progressEligible(compressionChild); ok {
		t.Error("compression turn on a child should not be eligible")
	}

	main := msg.ThreadInfo{
		SessionKey:     "telegram:1",
		State:          "running",
		ElapsedSec:     120,
		TotalToolCalls: 3,
		TurnWakeSource: string(msg.WakeTelegram),
	}
	if target, ok := progressEligible(main); !ok || target != "telegram:1" {
		t.Errorf("user-visible main turn not accepted: target=%q ok=%v", target, ok)
	}

	// Heartbeat / cron / compression / cross-session turns on a main key must
	// never produce user-facing progress notes.
	for _, src := range []msg.WakeSource{msg.WakeHeartbeat, msg.WakeCron, msg.WakeCompression, msg.WakeSession} {
		m := main
		m.TurnWakeSource = string(src)
		if _, ok := progressEligible(m); ok {
			t.Errorf("main turn with source %q should not be eligible", src)
		}
	}

	notRunning := child
	notRunning.State = "idle"
	if _, ok := progressEligible(notRunning); ok {
		t.Error("idle thread should not be eligible")
	}

	tooYoung := child
	tooYoung.ElapsedSec = progressMinElapsed - 1
	if _, ok := progressEligible(tooYoung); ok {
		t.Error("thread under min elapsed should not be eligible")
	}

	noCalls := child
	noCalls.TotalToolCalls = 0
	if _, ok := progressEligible(noCalls); ok {
		t.Error("turn with no tool calls should not be eligible")
	}

	sibling := main
	sibling.SessionKey = "telegram:1:progresssummary"
	if _, ok := progressEligible(sibling); ok {
		t.Error("internal sibling session should not be eligible (recursion guard)")
	}

	systemParent := child
	systemParent.SessionKey = "cron:job:threads:x"
	if _, ok := progressEligible(systemParent); ok {
		t.Error("child of non-user-facing parent should not be eligible")
	}
}

func TestBuildSummaryRequest(t *testing.T) {
	trace := make([]msg.ToolCallRecord, 0, progressMaxCalls+5)
	for range 5 {
		trace = append(trace, msg.ToolCallRecord{Name: "old_tool", ArgsSummary: `{"x":1}`})
	}
	for range progressMaxCalls - 2 {
		trace = append(trace, msg.ToolCallRecord{Name: "grep", ArgsSummary: `{"q":"a"}`})
	}
	trace = append(trace,
		msg.ToolCallRecord{Name: "web_search", ArgsSummary: `{"q":"quantum computing latest"}`, ResultPreview: "8 results: IBM unveils new chip"},
		msg.ToolCallRecord{Name: "fetch", ArgsSummary: `{"url":"y"}`, Error: true},
	)
	info := msg.ThreadInfo{
		SessionKey:     "cli:threads:find-x",
		ElapsedSec:     372,
		TotalToolCalls: len(trace),
		CurrentTool:    "read_file",
		OriginRequest:  "find the latest quantum computing news",
		ToolTrace:      trace,
	}
	out := buildSummaryRequest(info)

	if !strings.Contains(out, "find the latest quantum computing news") {
		t.Error("origin request missing")
	}
	if strings.Contains(out, "old_tool") {
		t.Errorf("call window not applied, oldest calls present")
	}
	if !strings.Contains(out, "oldest 5 omitted") {
		t.Error("omitted-count note missing")
	}
	if !strings.Contains(out, `web_search({"q":"quantum computing latest"})`) {
		t.Errorf("recent call with args missing: %s", out)
	}
	if !strings.Contains(out, "→ 8 results: IBM unveils new chip") {
		t.Error("result preview missing")
	}
	if !strings.Contains(out, "[FAILED]") {
		t.Error("error marker missing for failed call")
	}
	if !strings.Contains(out, "Currently executing: read_file") {
		t.Error("current tool line missing")
	}
	if !strings.Contains(out, "6m12s") {
		t.Error("humanized elapsed missing")
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

func TestSelectReports_ThrottleInFlightAndPrune(t *testing.T) {
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
	child.lastWakeSource = msg.WakeSession
	m.threads[child.sessionKey] = child

	ps := NewProgressScanner(m)
	now := time.Now()

	jobs := ps.selectReports(now)
	if len(jobs) != 1 || jobs[0].info.SessionKey != child.sessionKey || jobs[0].target != "telegram:99" {
		t.Fatalf("expected 1 job targeting telegram:99, got %+v", jobs)
	}
	if !ps.inFlight[child.sessionKey] {
		t.Fatal("selected job not marked in-flight")
	}

	// Same instant again → in-flight blocks a second job.
	if jobs := ps.selectReports(now); len(jobs) != 0 {
		t.Fatalf("in-flight thread re-selected: %+v", jobs)
	}

	// In-flight cleared but within the report interval → throttled.
	delete(ps.inFlight, child.sessionKey)
	if jobs := ps.selectReports(now.Add(progressInterval / 2)); len(jobs) != 0 {
		t.Fatalf("throttle failed: %+v", jobs)
	}

	// Past the interval → selected again.
	if jobs := ps.selectReports(now.Add(progressInterval + time.Second)); len(jobs) != 1 {
		t.Fatalf("expected re-selection after interval, got %+v", jobs)
	}
	delete(ps.inFlight, child.sessionKey)

	// Thread gone → throttle state pruned.
	delete(m.threads, child.sessionKey)
	ps.selectReports(now.Add(2 * progressInterval))
	if _, ok := ps.lastReport[child.sessionKey]; ok {
		t.Error("lastReport not pruned after thread stopped")
	}
}

func TestDeliverToAncestor_WakesParent(t *testing.T) {
	m := NewManager(nil)
	ancestor := &Thread{
		id:         "thread-anc",
		sessionKey: "telegram:99",
		state:      threadIdle,
		inbox:      make(chan *WakeMessage, 8),
		signal:     m.signal,
	}
	m.threads[ancestor.sessionKey] = ancestor

	ps := NewProgressScanner(m)
	info := msg.ThreadInfo{SessionKey: "telegram:99:threads:find-x", ElapsedSec: 372, TotalToolCalls: 14}
	ps.deliverToAncestor(info.SessionKey, "telegram:99", info, "⏳ searched 8 sources, reading the last one")

	if got := len(ancestor.inbox); got != 1 {
		t.Fatalf("expected 1 progress wake in ancestor inbox, got %d", got)
	}
	wake := <-ancestor.inbox
	if wake.Source != WakeProgress {
		t.Errorf("wake source = %q, want progress", wake.Source)
	}
	if !strings.Contains(wake.Message, "telegram:99:threads:find-x") {
		t.Error("wake body must include the child key (for stop-session)")
	}
	if !strings.Contains(wake.Message, "⏳ searched 8 sources") {
		t.Error("wake body must include the summary")
	}
	if !strings.Contains(wake.Message, "6m12s") || !strings.Contains(wake.Message, "14 steps") {
		t.Errorf("wake body missing elapsed/steps header: %s", wake.Message)
	}
}
