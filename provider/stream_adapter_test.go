// provider/stream_adapter_test.go
package provider

import (
	"context"
	"io"
	"testing"
)

func TestStreamAdapter_CollectDeltas(t *testing.T) {
	resp := &Response{}
	adapter := newStreamAdapter(context.Background(), resp)

	// Simulate provider goroutine.
	go func() {
		adapter.EmitText("hello")
		adapter.EmitText(" world")
		adapter.EmitToolCall("search")
		adapter.Finish()
	}()

	result := adapter.Result()
	stream := result.(StreamChatResult)

	var texts []string
	var toolName string
	for {
		d, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch d.Type {
		case DeltaText:
			texts = append(texts, d.Text)
		case DeltaToolCall:
			toolName = d.ToolName
		}
	}

	if len(texts) != 2 || texts[0] != "hello" || texts[1] != " world" {
		t.Errorf("texts = %v", texts)
	}
	if toolName != "search" {
		t.Errorf("toolName = %q", toolName)
	}
}

func TestStreamAdapter_CancelStopsRecv(t *testing.T) {
	resp := &Response{}
	adapter := newStreamAdapter(context.Background(), resp)

	result := adapter.Result()
	stream := result.(StreamChatResult)

	// Cancel before goroutine finishes — Recv should return EOF.
	stream.Cancel()

	// Finish the goroutine (EmitText on cancelled adapter is a no-op).
	adapter.EmitText("late")
	adapter.Finish()

	_, err := stream.Recv()
	if err != io.EOF {
		t.Errorf("expected EOF after cancel, got %v", err)
	}
}
