package obs

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/linanwx/nagobot/logger"
)

// jsonlExporter is the SpanExporter that makes this whole package local: it
// turns finished spans into SpanRecord lines instead of shipping them over
// OTLP. Swapping in otlptracegrpc here is the one change that would point the
// existing instrumentation at Jaeger or Tempo.
type jsonlExporter struct {
	store *Store
}

func newJSONLExporter(store *Store) *jsonlExporter { return &jsonlExporter{store: store} }

func (e *jsonlExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	records := make([]SpanRecord, 0, len(spans))
	for _, s := range spans {
		records = append(records, toRecord(s))
	}
	if err := e.store.Write(records); err != nil {
		// Telemetry must never take the daemon down, but it must not fail
		// silently either — a traces file that quietly stops growing reads as
		// "nothing happened".
		logger.Warn("obs: failed to write spans", "count", len(records), "err", err)
	}
	return nil
}

func (e *jsonlExporter) Shutdown(context.Context) error { return nil }

func toRecord(s sdktrace.ReadOnlySpan) SpanRecord {
	sc := s.SpanContext()
	rec := SpanRecord{
		Timestamp: s.StartTime(),
		TraceID:   sc.TraceID().String(),
		SpanID:    sc.SpanID().String(),
		Name:      s.Name(),
		DurMs:     s.EndTime().Sub(s.StartTime()).Milliseconds(),
	}
	if parent := s.Parent(); parent.IsValid() {
		rec.ParentID = parent.SpanID().String()
	}
	if st := s.Status(); st.Code == codes.Error {
		rec.Status = "error"
		rec.Error = st.Description
	}

	if attrs := s.Attributes(); len(attrs) > 0 {
		rec.Attrs = make(map[string]any, len(attrs))
		for _, kv := range attrs {
			rec.Attrs[string(kv.Key)] = attrValue(kv.Value)
		}
	}

	// Links carry the OTHER messages that merged into this turn. Only the trace
	// id is kept: it is what you grep for to find the merged-away message's own
	// ingest span, and the link's span id adds nothing on top of that.
	for _, l := range s.Links() {
		rec.Links = append(rec.Links, l.SpanContext.TraceID().String())
	}
	return rec
}

func attrValue(v attribute.Value) any {
	switch v.Type() {
	case attribute.BOOL:
		return v.AsBool()
	case attribute.INT64:
		return v.AsInt64()
	case attribute.FLOAT64:
		return v.AsFloat64()
	default:
		return v.AsString()
	}
}
