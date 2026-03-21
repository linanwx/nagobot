package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/linanwx/nagobot/provider"
)

func TestReadLastMessage(t *testing.T) {
	dir := t.TempDir()

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(dir, "empty.jsonl")
		os.WriteFile(path, []byte{}, 0644)
		_, err := ReadLastMessage(path)
		if err == nil {
			t.Fatal("expected error for empty file")
		}
	})

	t.Run("single message", func(t *testing.T) {
		path := filepath.Join(dir, "single.jsonl")
		msg := provider.Message{Role: "user", Content: "hello", Timestamp: time.Now()}
		data, _ := json.Marshal(msg)
		os.WriteFile(path, append(data, '\n'), 0644)
		got, err := ReadLastMessage(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.Role != "user" || got.Content != "hello" {
			t.Fatalf("unexpected: %+v", got)
		}
	})

	t.Run("multiple messages returns last", func(t *testing.T) {
		path := filepath.Join(dir, "multi.jsonl")
		var buf bytes.Buffer
		for _, role := range []string{"user", "assistant", "user", "assistant"} {
			msg := provider.Message{Role: role, Content: role + "-content", Timestamp: time.Now()}
			data, _ := json.Marshal(msg)
			buf.Write(data)
			buf.WriteByte('\n')
		}
		os.WriteFile(path, buf.Bytes(), 0644)
		got, err := ReadLastMessage(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.Role != "assistant" || got.Content != "assistant-content" {
			t.Fatalf("unexpected: %+v", got)
		}
	})

	t.Run("assistant with tool_calls", func(t *testing.T) {
		path := filepath.Join(dir, "toolcalls.jsonl")
		msg := provider.Message{
			Role:      "assistant",
			Content:   "thinking",
			ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read_file"}}},
			Timestamp: time.Now(),
		}
		data, _ := json.Marshal(msg)
		os.WriteFile(path, append(data, '\n'), 0644)
		got, err := ReadLastMessage(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(got.ToolCalls))
		}
	})

	t.Run("truncated last line skipped", func(t *testing.T) {
		path := filepath.Join(dir, "truncated.jsonl")
		msg := provider.Message{Role: "user", Content: "valid", Timestamp: time.Now()}
		data, _ := json.Marshal(msg)
		data = append(data, '\n')
		data = append(data, []byte(`{"role":"assistant","content":"trun`)...) // truncated
		os.WriteFile(path, data, 0644)
		got, err := ReadLastMessage(path)
		if err != nil {
			t.Fatal(err)
		}
		// Should return the last VALID message, not the truncated one.
		if got.Role != "user" || got.Content != "valid" {
			t.Fatalf("unexpected: %+v", got)
		}
	})
}
