package obs

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// initForTest installs a real tracer writing into a temp dir and returns a
// function that flushes and reads back the recorded spans.
func initForTest(t *testing.T) func() []SpanRecord {
	t.Helper()
	dir := t.TempDir()
	if err := Init(dir, true); err != nil {
		t.Fatalf("Init: %v", err)
	}
	store := NewStore(dir)
	t.Cleanup(func() { Shutdown(context.Background()) })

	return func() []SpanRecord {
		Shutdown(context.Background()) // forces a flush of the batch processor
		f, err := os.Open(store.Path())
		if err != nil {
			t.Fatalf("open traces: %v", err)
		}
		defer f.Close()
		var out []SpanRecord
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var r SpanRecord
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				t.Fatalf("bad JSONL line %q: %v", sc.Text(), err)
			}
			out = append(out, r)
		}
		return out
	}
}

// TestLenNeverRecordsTheText is the structural half of "traces carry no message
// content": Len is the ONLY way a body may influence a span, and it must keep
// the size while dropping the text.
func TestLenNeverRecordsTheText(t *testing.T) {
	read := initForTest(t)

	secret := "用户的私人对话内容 with a password hunter2 in the middle"
	_, span := Start(context.Background(), "ingest", Len("text", secret))
	span.End()

	spans := read()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	raw, _ := json.Marshal(spans[0])
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "私人对话") {
		t.Fatalf("span leaked message content: %s", raw)
	}
	// Rune count, not byte count — otherwise every CJK conversation reports
	// three times its real length.
	if got := spans[0].Attrs["text_len"]; got != float64(len([]rune(secret))) {
		t.Fatalf("text_len = %v, want %d", got, len([]rune(secret)))
	}
}

// TestStrTruncatesOverLongValues pins the runtime backstop. Str is for
// identifiers and enums; a call site that passes content gets truncated and
// warned rather than writing the body to disk. TestNoAttrExceedsCap is the
// compile-time-ish half — this is what happens if one slips through.
func TestStrTruncatesOverLongValues(t *testing.T) {
	read := initForTest(t)

	long := strings.Repeat("私", maxAttrChars+50)
	_, span := Start(context.Background(), "turn", Str("session_key", long))
	span.End()

	spans := read()
	got, _ := spans[0].Attrs["session_key"].(string)
	if n := len([]rune(got)); n != maxAttrChars {
		t.Fatalf("truncated to %d runes, want %d", n, maxAttrChars)
	}
}

// TestNoAttrExceedsCap sweeps everything the recorded spans carry. It is the
// generic guard: any future call site that starts passing a message body into
// Str trips this without anyone remembering to write a test for it.
func TestNoAttrExceedsCap(t *testing.T) {
	read := initForTest(t)

	_, s := Start(context.Background(), "turn",
		Str("session_key", "web:2118acc7"),
		Str("agent", "soul"),
		Str("model", "gpt-5.6-luna"),
		Int("wait_ms", 12),
		Bool("stream", true),
		Len("text", strings.Repeat("x", 5000)),
	)
	s.End()

	for _, span := range read() {
		for k, v := range span.Attrs {
			str, ok := v.(string)
			if !ok {
				continue
			}
			if n := len([]rune(str)); n > maxAttrChars {
				t.Fatalf("span %q attr %q is %d runes — content leak", span.Name, k, n)
			}
		}
	}
}

// TestTraceparentSurvivesTheQueue is the property the whole design rests on:
// Manager.Wake takes no context, so a wake carries its trace as a string. If
// this round trip breaks, every turn silently becomes its own root and the
// queue wait — the reason for tracing in the first place — disappears.
func TestTraceparentSurvivesTheQueue(t *testing.T) {
	read := initForTest(t)

	// Producer side: a span, serialized the way the dispatcher does it.
	pctx, parent := Start(context.Background(), "ingest")
	tp := Traceparent(pctx)
	if tp == "" {
		t.Fatal("Traceparent returned empty for an active span")
	}
	parent.End()

	// Consumer side: a different goroutine's context entirely, the way RunOnce
	// receives the manager's process-lifetime ctx.
	cctx := ContextWith(context.Background(), tp)
	_, child := Start(cctx, "turn")
	child.End()

	spans := read()
	byName := map[string]SpanRecord{}
	for _, s := range spans {
		byName[s.Name] = s
	}
	ingest, turn := byName["ingest"], byName["turn"]
	if turn.TraceID != ingest.TraceID {
		t.Fatalf("turn joined trace %s, want %s", turn.TraceID, ingest.TraceID)
	}
	if turn.ParentID != ingest.SpanID {
		t.Fatalf("turn parent = %s, want ingest span %s", turn.ParentID, ingest.SpanID)
	}
}

// TestMergedWakesBecomeLinks covers tryMerge's shape: N messages fold into one
// turn, so the turn cannot be a child of all of them. Links are how the
// merged-away messages stay reachable.
func TestMergedWakesBecomeLinks(t *testing.T) {
	read := initForTest(t)

	var merged []string
	var wantTraces []string
	for range 2 {
		ctx, s := Start(context.Background(), "ingest")
		merged = append(merged, Traceparent(ctx))
		wantTraces = append(wantTraces, s.span.SpanContext().TraceID().String())
		s.End()
	}

	mainCtx, mainSpan := Start(context.Background(), "ingest")
	mainTP := Traceparent(mainCtx)
	mainSpan.End()

	_, turn := StartLinked(ContextWith(context.Background(), mainTP), "turn", merged)
	turn.End()

	for _, s := range read() {
		if s.Name != "turn" {
			continue
		}
		if len(s.Links) != 2 {
			t.Fatalf("turn has %d links, want 2", len(s.Links))
		}
		for _, want := range wantTraces {
			if !slices.Contains(s.Links, want) {
				t.Fatalf("links %v missing merged trace %s", s.Links, want)
			}
		}
		return
	}
	t.Fatal("no turn span recorded")
}

// TestDisabledTracingIsSafe pins that every call site stays valid with tracing
// off — the no-op tracer must not panic and must write nothing.
func TestDisabledTracingIsSafe(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, false); err != nil {
		t.Fatalf("Init(disabled): %v", err)
	}
	t.Cleanup(func() { Shutdown(context.Background()) })

	if Enabled() {
		t.Fatal("Enabled() true after disabled init")
	}
	ctx, span := Start(context.Background(), "turn", Str("k", "v"), Len("text", "hello"))
	span.Set(Int("n", 1))
	span.Fail(context.Canceled)
	span.End()
	if tp := Traceparent(ctx); tp != "" {
		t.Fatalf("disabled tracer produced traceparent %q", tp)
	}
	if _, err := os.Stat(NewStore(dir).Path()); !os.IsNotExist(err) {
		t.Fatal("disabled tracing wrote a traces file")
	}
}

// TestMalformedTraceparentStartsFreshTrace covers the wake that carries no
// trace at all (a cron seed, a hand-built WakeMessage, an older persisted
// wake). It must open a new root, never fail the turn.
func TestMalformedTraceparentStartsFreshTrace(t *testing.T) {
	read := initForTest(t)

	for _, tp := range []string{"", "garbage", "00-notahex-alsonot-01"} {
		_, s := Start(ContextWith(context.Background(), tp), "turn")
		s.End()
	}

	spans := read()
	if len(spans) != 3 {
		t.Fatalf("expected 3 spans, got %d", len(spans))
	}
	for _, s := range spans {
		if s.ParentID != "" {
			t.Fatalf("span with malformed traceparent got parent %q", s.ParentID)
		}
	}
}

