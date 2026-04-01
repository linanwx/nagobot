// provider/stream_adapter.go
package provider

import "context"

// streamAdapter bridges provider-internal streaming (goroutine writing deltas)
// with the StreamChatResult interface (consumer pulling via Recv).
//
// Usage in a provider:
//
//	resp := &Response{}
//	adapter := newStreamAdapter(ctx, resp)
//	go func() {
//	    // read SSE / SDK stream, call adapter.EmitText / EmitToolCall
//	    adapter.Finish()
//	}()
//	return adapter.Result(), nil
type streamAdapter struct {
	ch     chan StreamDelta
	resp   *Response
	err    error // set by producer goroutine on stream error; read by Wait() after drain
	ctx    context.Context
	cancel context.CancelFunc
}

// newStreamAdapter creates an adapter. The provider goroutine writes deltas
// via Emit* methods and calls Finish() when done. The consumer reads via
// the returned StreamChatResult.
func newStreamAdapter(parentCtx context.Context, resp *Response) *streamAdapter {
	ctx, cancel := context.WithCancel(parentCtx)
	return &streamAdapter{
		ch:     make(chan StreamDelta, 64),
		resp:   resp,
		ctx:    ctx,
		cancel: cancel,
	}
}

// EmitText sends a text delta. No-op if cancelled or finished.
func (a *streamAdapter) EmitText(text string) {
	if text == "" {
		return
	}
	select {
	case a.ch <- StreamDelta{Type: DeltaText, Text: text}:
	case <-a.ctx.Done():
	}
}

// EmitToolCall sends a tool-call-start delta. No-op if cancelled or finished.
func (a *streamAdapter) EmitToolCall(name string) {
	select {
	case a.ch <- StreamDelta{Type: DeltaToolCall, ToolName: name}:
	case <-a.ctx.Done():
	}
}

// SetError records a stream error. Call before Finish().
// Wait() will return this error after draining the channel.
func (a *streamAdapter) SetError(err error) {
	a.err = err
}

// Finish closes the delta channel. Must be called exactly once by the producer.
func (a *streamAdapter) Finish() {
	close(a.ch)
}

// Result returns the StreamChatResult for the consumer.
func (a *streamAdapter) Result() StreamChatResult {
	return newStreamResultFull(a.ch, a.resp, a.cancel, &a.err)
}
