// provider/chat_result_test.go
package provider

import (
	"io"
	"testing"
)

func TestBasicResult_Wait(t *testing.T) {
	resp := &Response{Content: "hello", Usage: Usage{TotalTokens: 10}}
	result := NewBasicResult(resp)

	// Should not be a StreamChatResult.
	if _, ok := result.(StreamChatResult); ok {
		t.Fatal("basicResult should not implement StreamChatResult")
	}

	got, err := result.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello" {
		t.Errorf("got %q, want %q", got.Content, "hello")
	}
}

func TestStreamResult_Recv(t *testing.T) {
	ch := make(chan StreamDelta, 3)
	ch <- StreamDelta{Type: DeltaToolCall, ToolName: "search"}
	ch <- StreamDelta{Type: DeltaText, Text: "hello"}
	ch <- StreamDelta{Type: DeltaText, Text: " world"}
	close(ch)

	resp := &Response{Content: "hello world", Usage: Usage{TotalTokens: 5}}
	result := newStreamResultFull(ch, resp, nil, nil)

	// Should be a StreamChatResult.
	stream, ok := result.(StreamChatResult)
	if !ok {
		t.Fatal("should implement StreamChatResult")
	}

	// Recv all deltas.
	var deltas []StreamDelta
	for {
		d, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		deltas = append(deltas, d)
	}
	if len(deltas) != 3 {
		t.Fatalf("got %d deltas, want 3", len(deltas))
	}
	if deltas[0].Type != DeltaToolCall || deltas[0].ToolName != "search" {
		t.Errorf("delta[0] = %+v, want ToolCall/search", deltas[0])
	}

	// Wait returns the full response.
	got, err := result.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello world" {
		t.Errorf("got %q, want %q", got.Content, "hello world")
	}
}

func TestStreamResult_WaitDrainsStream(t *testing.T) {
	ch := make(chan StreamDelta, 2)
	ch <- StreamDelta{Type: DeltaText, Text: "a"}
	ch <- StreamDelta{Type: DeltaText, Text: "b"}
	close(ch)

	resp := &Response{Content: "ab"}
	result := newStreamResultFull(ch, resp, nil, nil)

	// Call Wait without Recv — should still work (drains internally).
	got, err := result.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "ab" {
		t.Errorf("got %q, want %q", got.Content, "ab")
	}
}
