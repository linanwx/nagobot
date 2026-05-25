package channel

import (
	"context"
	"testing"
	"time"
)

type fakeFeishuWSClient struct {
	closed chan struct{}
}

func (f *fakeFeishuWSClient) Start(context.Context) error {
	select {}
}

func (f *fakeFeishuWSClient) Close() {
	close(f.closed)
}

func TestFeishuStopDoesNotWaitForWebSocketStartReturn(t *testing.T) {
	ws := &fakeFeishuWSClient{closed: make(chan struct{})}
	f := &FeishuChannel{
		messages: make(chan *Message),
		done:     make(chan struct{}),
		wsClient: ws,
	}
	f.cancel = func() {}

	stopped := make(chan struct{})
	go func() {
		_ = f.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("Stop blocked waiting for websocket Start to return")
	}

	select {
	case <-ws.closed:
	default:
		t.Fatal("Stop did not close websocket client")
	}

	select {
	case <-f.done:
	default:
		t.Fatal("Stop did not close done channel")
	}

	select {
	case _, ok := <-f.messages:
		if ok {
			t.Fatal("Stop did not close messages channel")
		}
	default:
		t.Fatal("Stop did not close messages channel")
	}
}
