package thread

import (
	"testing"

	"github.com/linanwx/nagobot/tools"
)

func newIDTestThread() *Thread {
	return &Thread{
		sessionKey: "web:idtest",
		mgr:        NewManager(&ThreadConfig{ProviderName: "deepseek", ModelName: "deepseek-v4-flash"}),
		tools:      tools.NewRegistry(),
	}
}

// The whole point of a client-minted id is that it reaches disk unchanged:
// buildMessageHistory stamps it on the turn's user message, and the write-ahead
// Append persists that message as-is (session.EnsureMessageIDs only fills ids
// that are empty). Assert the first half here; TestAppendPreservesClientAssignedID
// in the session package holds the second.
func TestBuildMessageHistory_KeepsClientMessageID(t *testing.T) {
	th := newIDTestThread()
	_, turnUserMessages := th.buildMessageHistory(t.Context(), "sys", "hello", "web-abc123", nil, nil)
	if len(turnUserMessages) == 0 {
		t.Fatal("expected a turn user message")
	}
	if got := turnUserMessages[0].ID; got != "web-abc123" {
		t.Errorf("client message id not carried onto the user message: got %q", got)
	}
}

// No id supplied is the normal case for every channel that does not mint one —
// the field must stay empty so the session store assigns its own.
func TestBuildMessageHistory_EmptyIDLeavesAssignmentToStore(t *testing.T) {
	th := newIDTestThread()
	_, turnUserMessages := th.buildMessageHistory(t.Context(), "sys", "hello", "", nil, nil)
	if len(turnUserMessages) == 0 {
		t.Fatal("expected a turn user message")
	}
	if got := turnUserMessages[0].ID; got != "" {
		t.Errorf("id should be left empty for the store to fill, got %q", got)
	}
}
