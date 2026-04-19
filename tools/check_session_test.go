package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type mockSessionChecker struct {
	statuses map[string]SessionStatusInfo
}

func (m *mockSessionChecker) SessionStatus(key string) SessionStatusInfo {
	if s, ok := m.statuses[key]; ok {
		return s
	}
	return SessionStatusInfo{SessionKey: key}
}

func runCheckSession(t *testing.T, checker SessionChecker, argsJSON string) string {
	t.Helper()
	tool := NewCheckSessionTool(checker)
	return tool.Run(context.Background(), json.RawMessage(argsJSON))
}

func TestCheckSession_NotFound(t *testing.T) {
	checker := &mockSessionChecker{statuses: map[string]SessionStatusInfo{}}
	res := runCheckSession(t, checker, `{"session_key": "telegram:nonexistent"}`)
	if !strings.Contains(res, "Session not found") {
		t.Errorf("expected not-found message, got: %s", res)
	}
	if !strings.Contains(res, "thread_active") {
		t.Errorf("expected thread_active field, got: %s", res)
	}
}

func TestCheckSession_DiskOnly(t *testing.T) {
	checker := &mockSessionChecker{statuses: map[string]SessionStatusInfo{
		"cli:threads:idle": {
			SessionKey:    "cli:threads:idle",
			Exists:        true,
			Agent:         "search",
			MessageCount:  42,
			FileSizeBytes: 12345,
			LastModified:  time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC),
		},
	}}
	res := runCheckSession(t, checker, `{"session_key": "cli:threads:idle"}`)
	for _, want := range []string{"agent: search", "message_count: 42", "thread_active: false", "no thread is currently loaded"} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in result, got: %s", want, res)
		}
	}
}

func TestCheckSession_ThreadActive(t *testing.T) {
	checker := &mockSessionChecker{statuses: map[string]SessionStatusInfo{
		"cli:threads:hot": {
			SessionKey:   "cli:threads:hot",
			Exists:       true,
			Agent:        "search",
			MessageCount: 5,
			ThreadActive: true,
			Thread: &ThreadInfo{
				ID:             "thread-abc123",
				SessionKey:     "cli:threads:hot",
				State:          "running",
				Pending:        2,
				Iterations:     3,
				TotalToolCalls: 7,
				CurrentTool:    "web_search",
				ElapsedSec:     12,
			},
		},
	}}
	res := runCheckSession(t, checker, `{"session_key": "cli:threads:hot"}`)
	for _, want := range []string{
		"thread_active: true",
		"thread_id: thread-abc123",
		"thread_state: running",
		"thread_current_tool: web_search",
		"Thread is running",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q, got: %s", want, res)
		}
	}
}

func TestCheckSession_MissingKey(t *testing.T) {
	checker := &mockSessionChecker{}
	res := runCheckSession(t, checker, `{}`)
	if !strings.Contains(res, "session_key") {
		t.Errorf("expected session_key required error, got: %s", res)
	}
}

func TestCheckSession_EmptyKey(t *testing.T) {
	checker := &mockSessionChecker{}
	res := runCheckSession(t, checker, `{"session_key": "  "}`)
	if !strings.Contains(res, "required") {
		t.Errorf("expected required-error, got: %s", res)
	}
}
