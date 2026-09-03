package thread

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

// --- a provider that replays a fixed delta script ---

type scriptedStream struct {
	deltas []provider.StreamDelta
	i      int
	resp   *provider.Response
}

func (s *scriptedStream) Recv() (provider.StreamDelta, error) {
	if s.i >= len(s.deltas) {
		return provider.StreamDelta{}, io.EOF
	}
	d := s.deltas[s.i]
	s.i++
	return d, nil
}
func (s *scriptedStream) Wait() (*provider.Response, error) { return s.resp, nil }
func (s *scriptedStream) Cancel()                           {}

type scriptedProvider struct{ stream *scriptedStream }

func (p *scriptedProvider) Chat(context.Context, *provider.Request) (provider.ChatResult, error) {
	return p.stream, nil
}

// TestOnlyVisibleTextMarksTheTurnAsAnswering pins which delta kind stamps the
// turn. Reasoning must NOT: during a long think nothing has reached the user,
// which is precisely the wait a progress note exists to break. Stamping it
// would silence the scanner exactly when it is most useful.
func TestOnlyVisibleTextMarksTheTurnAsAnswering(t *testing.T) {
	m := &ExecMetrics{TurnStart: time.Now()}
	fp := &scriptedProvider{stream: &scriptedStream{
		deltas: []provider.StreamDelta{
			{Type: provider.DeltaReasoning, Text: "weighing the options"},
			{Type: provider.DeltaReasoning, Text: "still weighing"},
		},
		resp: &provider.Response{},
	}}
	r := &Runner{provider: fp, metrics: m}

	if _, err := r.callLLM(context.Background(), &provider.Request{}); err != nil {
		t.Fatalf("callLLM (reasoning only): %v", err)
	}
	if !m.LastTextDeltaAt.IsZero() {
		t.Fatal("a reasoning delta marked the turn as answering; a long think is when a progress note matters most")
	}

	// The same turn now emits real text.
	fp.stream = &scriptedStream{
		deltas: []provider.StreamDelta{{Type: provider.DeltaText, Text: "here is what I found"}},
		resp:   &provider.Response{Content: "here is what I found"},
	}
	if _, err := r.callLLM(context.Background(), &provider.Request{}); err != nil {
		t.Fatalf("callLLM (text): %v", err)
	}
	if m.LastTextDeltaAt.IsZero() {
		t.Fatal("a user-visible text delta did not mark the turn as answering")
	}
}

// streamingThread builds a thread that progressEligible already accepts, so the
// only variable under test is the streaming stamp.
func streamingThread(m *Manager, lastText time.Time) *Thread {
	th := &Thread{
		id:         "thread-child",
		sessionKey: "telegram:99:threads:find-x",
		state:      threadRunning,
		inbox:      make(chan *WakeMessage, 8),
		signal:     m.signal,
		execMetrics: &ExecMetrics{
			TurnStart:       time.Now().Add(-2 * time.Minute),
			TotalToolCalls:  3,
			ToolCalls:       []msg.ToolCallRecord{{Name: "web_search"}},
			LastTextDeltaAt: lastText,
		},
	}
	th.lastWakeSource = msg.WakeSession
	m.threads[th.sessionKey] = th
	return th
}

// TestSelectReports_HoldsOffWhileTheAnswerIsArriving is the whole point of the
// stamp: a note that interrupts a reply the user is already reading is noise,
// not progress.
func TestSelectReports_HoldsOffWhileTheAnswerIsArriving(t *testing.T) {
	m := NewManager(nil)
	now := time.Now()
	streamingThread(m, now)
	ps := NewProgressScanner(m)

	if jobs := ps.selectReports(now); len(jobs) != 0 {
		t.Fatalf("reported while text was streaming: %+v", jobs)
	}
	// Still inside the quiet window — the model paused between deltas, which is
	// not the same as having stopped.
	if jobs := ps.selectReports(now.Add(progressStreamingQuiet - time.Second)); len(jobs) != 0 {
		t.Fatalf("reported during a gap between deltas: %+v", jobs)
	}
	// Text stopped and the turn ran on: this is a real silent wait again.
	if jobs := ps.selectReports(now.Add(progressStreamingQuiet + time.Second)); len(jobs) != 1 {
		t.Fatalf("never recovered after streaming stopped, got %+v", jobs)
	}
}

// TestSelectReports_PreambleThenToolStillReports is the measured majority case
// the naive rule would have broken. Across the fleet 8.5% of tool-calling
// assistant messages open with prose (p50 72 runes) and then call a tool, so
// "text appeared" must not suppress anything permanently — only "text is still
// appearing" may.
func TestSelectReports_PreambleThenToolStillReports(t *testing.T) {
	m := NewManager(nil)
	now := time.Now()
	// A short preamble finished a minute ago; a slow tool has run ever since.
	streamingThread(m, now.Add(-time.Minute))
	ps := NewProgressScanner(m)

	if jobs := ps.selectReports(now); len(jobs) != 1 {
		t.Fatalf("a finished preamble suppressed the report, got %+v", jobs)
	}
}

// TestSelectReports_UnstampedTurnIsUnaffected pins the fail-safe direction. A
// non-streaming provider emits no deltas at all, so the stamp stays zero and
// must change nothing — never suppress on the strength of a signal we do not
// actually have.
func TestSelectReports_UnstampedTurnIsUnaffected(t *testing.T) {
	m := NewManager(nil)
	streamingThread(m, time.Time{})
	ps := NewProgressScanner(m)

	if jobs := ps.selectReports(time.Now()); len(jobs) != 1 {
		t.Fatalf("a turn with no streaming stamp was suppressed, got %+v", jobs)
	}
}

// TestSelectReports_SuppressedThreadKeepsItsThrottleState guards the ordering
// inside selectReports: a streaming thread is still running, so it must stay in
// `seen`. Skipping it before the seen mark would prune its lastReport and let
// it report the instant streaming stopped, ignoring the 60s gap.
func TestSelectReports_SuppressedThreadKeepsItsThrottleState(t *testing.T) {
	m := NewManager(nil)
	now := time.Now()
	th := streamingThread(m, now.Add(-time.Hour)) // not streaming yet
	ps := NewProgressScanner(m)

	if jobs := ps.selectReports(now); len(jobs) != 1 {
		t.Fatalf("setup: expected an initial report, got %+v", jobs)
	}
	delete(ps.inFlight, th.sessionKey)

	// It starts answering; the next sweep must suppress without forgetting that
	// it reported a moment ago.
	th.execMetrics.LastTextDeltaAt = now
	if jobs := ps.selectReports(now.Add(time.Second)); len(jobs) != 0 {
		t.Fatalf("reported while streaming: %+v", jobs)
	}
	if _, ok := ps.lastReport[th.sessionKey]; !ok {
		t.Fatal("throttle state pruned for a thread that is merely streaming")
	}
}
