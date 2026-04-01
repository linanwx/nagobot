// provider/chat_result.go
package provider

import (
	"io"
	"sync/atomic"
)

// DeltaType identifies the kind of streaming delta.
type DeltaType int

const (
	DeltaText     DeltaType = iota // Text content delta
	DeltaToolCall                  // First tool call detected
)

// StreamDelta is a single unit emitted by a streaming provider.
type StreamDelta struct {
	Type     DeltaType
	Text     string // DeltaText: the text chunk
	ToolName string // DeltaToolCall: name of the first tool
}

// ChatResult is returned by Provider.Chat(). All providers return at least this.
type ChatResult interface {
	// Wait blocks until the response is complete and returns it.
	// For streaming results, Wait drains remaining deltas first.
	Wait() (*Response, error)
}

// StreamChatResult extends ChatResult with streaming capabilities.
// Runner uses type assertion to check if streaming is available.
type StreamChatResult interface {
	ChatResult
	// Recv returns the next delta. Returns io.EOF when the stream is done.
	Recv() (StreamDelta, error)
	// Cancel aborts the stream early (e.g. repetition detection).
	Cancel()
}

// basicResult wraps a completed Response for non-streaming providers.
type basicResult struct {
	resp *Response
}

// NewBasicResult wraps a completed *Response as a ChatResult.
func NewBasicResult(resp *Response) ChatResult {
	return &basicResult{resp: resp}
}

func (r *basicResult) Wait() (*Response, error) {
	return r.resp, nil
}

// streamResult wraps a delta channel and final Response.
type streamResult struct {
	ch        <-chan StreamDelta
	resp      *Response
	cancel    func()  // optional cancel function
	errPtr    *error  // optional: points to producer's error; read after channel drain
	cancelled atomic.Bool
}

// newStreamResultFull creates a StreamChatResult with cancel and error propagation.
// errPtr points to the producer's error field; it is read only after the channel is
// drained (happens-before guaranteed by channel close).
func newStreamResultFull(ch <-chan StreamDelta, resp *Response, cancel func(), errPtr *error) StreamChatResult {
	return &streamResult{ch: ch, resp: resp, cancel: cancel, errPtr: errPtr}
}

func (r *streamResult) Recv() (StreamDelta, error) {
	if r.cancelled.Load() {
		// Drain remaining buffered deltas after cancel.
		for range r.ch {
		}
		return StreamDelta{}, io.EOF
	}
	d, ok := <-r.ch
	if !ok {
		return StreamDelta{}, io.EOF
	}
	return d, nil
}

func (r *streamResult) Wait() (*Response, error) {
	// Drain remaining deltas.
	for range r.ch {
	}
	// After drain, channel is closed — producer has finished writing resp and err.
	if r.errPtr != nil && *r.errPtr != nil {
		return r.resp, *r.errPtr
	}
	return r.resp, nil
}

func (r *streamResult) Cancel() {
	r.cancelled.Store(true)
	if r.cancel != nil {
		r.cancel()
	}
}
