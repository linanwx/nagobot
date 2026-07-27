// Package obs provides request tracing for the message pipeline: one trace per
// inbound message, from the channel that received it through the queue, the
// turn, every LLM call and every tool execution.
//
// Two decisions shape the whole package:
//
//   - **Spans never carry message content.** There is no attribute setter that
//     takes free-form text. Sizes ride as counts (Len), everything else is an
//     identifier, an enum, a number or a bool, and every string value is capped
//     (see attrs.go). The traces file is therefore safe to hand to an LLM for
//     analysis, which is the only reason it exists.
//   - **The exporter writes local JSONL, but the instrumentation is OpenTelemetry.**
//     Call sites use the standard API, so pointing this at Jaeger/Tempo later is
//     an exporter swap in Init, not a rewrite. At the measured volume (~150
//     turns/day across the deployment, ~1.6K spans/day) a local file is the
//     right backend; nothing about the call sites assumes that.
package obs

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/linanwx/nagobot/logger"
)

const tracerName = "github.com/linanwx/nagobot"

var (
	mu       sync.RWMutex
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer = noop.NewTracerProvider().Tracer(tracerName)

	// propagator serializes span context across the wake queue. Manager.Wake
	// takes no context — the queue is a hard context break — so the trace rides
	// on WakeMessage as a W3C traceparent string instead. Using the standard
	// format rather than an ad-hoc pair of ids means a wake could cross a
	// process boundary unchanged if it ever needs to.
	propagator = propagation.TraceContext{}
)

// Init installs the JSONL tracer provider. Spans land in dir/traces.jsonl,
// alongside the metrics store's turns.jsonl and under the same retention.
//
// Disabled (or failed) init leaves the no-op tracer in place, so every call
// site stays valid and costs a nil check. Init is not safe to call concurrently
// with Start; call it once during startup.
func Init(dir string, enabled bool) error {
	if !enabled {
		logger.Info("tracing disabled")
		return nil
	}

	store := NewStore(dir)
	store.Rotate()

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(newJSONLExporter(store)),
		// Volume is ~1.6K spans/day. Sampling would only add a reason for a
		// trace to be missing exactly when someone goes looking for it.
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	mu.Lock()
	provider = tp
	tracer = tp.Tracer(tracerName)
	mu.Unlock()

	logger.Info("tracing enabled", "file", store.Path())
	return nil
}

// Shutdown flushes buffered spans. The batch processor holds up to 5s of spans,
// so skipping this on exit loses the tail of the last conversation — which is
// the one a crash investigation wants most.
func Shutdown(ctx context.Context) {
	mu.Lock()
	tp := provider
	provider = nil
	tracer = noop.NewTracerProvider().Tracer(tracerName)
	mu.Unlock()

	if tp == nil {
		return
	}
	if err := tp.Shutdown(ctx); err != nil {
		logger.Warn("tracing shutdown failed", "err", err)
	}
}

// Enabled reports whether spans are being recorded.
func Enabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return provider != nil
}

// Start begins a span as a child of whatever is in ctx. The returned Span is
// always non-nil and always safe to use, including when tracing is off.
//
// Callers must End it. The usual shape:
//
//	ctx, span := obs.Start(ctx, "llm.call", obs.Str("provider", name))
//	defer span.End()
func Start(ctx context.Context, name string, attrs ...Attr) (context.Context, Span) {
	mu.RLock()
	t := tracer
	mu.RUnlock()

	ctx, s := t.Start(ctx, name)
	span := Span{span: s}
	span.Set(attrs...)
	return ctx, span
}

// StartLinked begins a span that also records causal links to other traces.
//
// This exists for tryMerge: consecutive same-source wakes fold into ONE turn,
// so a turn routinely has several originating messages. Parent-child cannot
// express that — it would force picking one arbitrary parent and dropping the
// rest of the causality. Links can.
func StartLinked(ctx context.Context, name string, traceparents []string, attrs ...Attr) (context.Context, Span) {
	mu.RLock()
	t := tracer
	mu.RUnlock()

	var links []trace.Link
	for _, tp := range traceparents {
		sc := trace.SpanContextFromContext(ContextWith(context.Background(), tp))
		if sc.IsValid() {
			links = append(links, trace.Link{SpanContext: sc})
		}
	}

	ctx, s := t.Start(ctx, name, trace.WithLinks(links...))
	span := Span{span: s}
	span.Set(attrs...)
	return ctx, span
}

// Traceparent serializes ctx's span context for transport across the wake
// queue. Returns "" when there is no active span.
func Traceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagator.Inject(ctx, carrier)
	return carrier.Get("traceparent")
}

// ContextWith restores a span context serialized by Traceparent. An empty or
// malformed value yields ctx unchanged, so a wake with no trace simply starts a
// new one rather than failing.
func ContextWith(ctx context.Context, traceparent string) context.Context {
	if traceparent == "" {
		return ctx
	}
	return propagator.Extract(ctx, propagation.MapCarrier{"traceparent": traceparent})
}
