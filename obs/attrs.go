package obs

import (
	"sync"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/linanwx/nagobot/logger"
)

// maxAttrChars caps every string attribute value.
//
// The cap is the structural half of "traces never record message content" — the
// other half is that no constructor here takes a message body in the first
// place. Identifiers, model names, agent names, tool names and enums all fit
// well inside it; anything that does not is a call site that leaked content,
// and TestNoAttrExceedsCap plus the per-call-site tests are what catch it.
//
// Runtime behaviour on an over-long value is truncate + Warn, not panic: losing
// the daemon over a telemetry attribute would be a worse failure than the leak
// it is guarding against. The Warn is what makes it non-silent.
const maxAttrChars = 200

// Attr is one span attribute. Constructed only through the helpers below —
// there is deliberately no constructor that accepts arbitrary text.
type Attr struct {
	kv attribute.KeyValue
}

var (
	warnedMu sync.Mutex
	warned   = map[string]bool{}
)

// Str records an identifier or enum: a session key, provider, model, agent,
// tool name, channel, wake source.
//
// It is NOT a general text setter. Values are capped at maxAttrChars and an
// over-long value logs a Warn — if that fires, the call site is passing content
// and must be changed to Len instead.
func Str(k, v string) Attr {
	if utf8.RuneCountInString(v) > maxAttrChars {
		warnedMu.Lock()
		first := !warned[k]
		warned[k] = true
		warnedMu.Unlock()
		if first {
			logger.Warn("obs: attribute value truncated — call site may be leaking content",
				"key", k, "runes", utf8.RuneCountInString(v), "cap", maxAttrChars)
		}
		v = truncateRunes(v, maxAttrChars)
	}
	return Attr{attribute.String(k, v)}
}

// Int records a count, size or duration in whatever unit the key names.
func Int(k string, v int) Attr { return Attr{attribute.Int(k, v)} }

// Int64 records a wide count.
func Int64(k string, v int64) Attr { return Attr{attribute.Int64(k, v)} }

// Bool records a flag.
func Bool(k string, v bool) Attr { return Attr{attribute.Bool(k, v)} }

// Len records the SIZE of a piece of text and throws the text away. This is the
// only way message bodies, prompts, tool arguments and tool results are allowed
// to appear in a trace. The attribute is named k+"_len" and counts runes, not
// bytes, so CJK conversations are not reported as three times their length.
func Len(k, text string) Attr {
	return Attr{attribute.Int(k+"_len", utf8.RuneCountInString(text))}
}

func truncateRunes(s string, n int) string {
	i := 0
	for idx := range s {
		if i == n {
			return s[:idx]
		}
		i++
	}
	return s
}

// Span wraps an OTel span with the narrowed surface this codebase uses. The
// zero value is usable and does nothing, which is what lets Start return
// unconditionally and call sites skip nil checks.
type Span struct {
	span trace.Span
}

// Set attaches attributes to the span.
func (s Span) Set(attrs ...Attr) {
	if s.span == nil || len(attrs) == 0 {
		return
	}
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		kvs = append(kvs, a.kv)
	}
	s.span.SetAttributes(kvs...)
}

// Fail marks the span as failed. The error's message is recorded — errors in
// this codebase are constructed from code, not from user text, so this is the
// one place a free string is admitted. Passing an error built by wrapping a
// message body would defeat that; don't.
func (s Span) Fail(err error) {
	if s.span == nil || err == nil {
		return
	}
	s.span.SetStatus(codes.Error, truncateRunes(err.Error(), maxAttrChars))
}

// FailMsg marks the span as failed with a fixed reason string (an enum, not a
// message). Use when there is no error value — a budget timeout, a rejected
// format.
func (s Span) FailMsg(reason string) {
	if s.span == nil {
		return
	}
	s.span.SetStatus(codes.Error, truncateRunes(reason, maxAttrChars))
}

// End closes the span.
func (s Span) End() {
	if s.span == nil {
		return
	}
	s.span.End()
}
