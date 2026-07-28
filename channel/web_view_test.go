package channel

import (
	"testing"

	"github.com/linanwx/nagobot/provider"
)

// storedFixture is one of each entry shape the chat projection treats
// differently, in the order a real session holds them.
func storedFixture() []provider.Message {
	return []provider.Message{
		{Role: "user", Content: "hello", Source: "web"},
		{Role: "assistant", Content: "hi", ReasoningContent: "live reasoning"},

		// A heartbeat turn compression already collapsed: the wake keeps a
		// marker, the body carries the flag.
		{Role: "user", Content: "wake", Source: "heartbeat", Compressed: "[heartbeat at 03:12 — trimmed]"},
		{Role: "assistant", Content: "dreamt", HeartbeatTrim: true},
		{Role: "tool", Name: "dispatch", Content: "turn-terminated-silent", HeartbeatTrim: true},

		// A progress turn that ended silently. Same flag despite the name.
		{Role: "user", Content: "wake", Source: "progress", Compressed: "[progress at 09:40 — trimmed]"},
		{Role: "assistant", Content: "still working", HeartbeatTrim: true},

		// A progress turn that actually spoke is never marked at all.
		{Role: "user", Content: "wake", Source: "progress"},
		{Role: "assistant", Content: "halfway through the migration"},

		// Old reasoning the bot no longer sends. The entry still speaks.
		{Role: "assistant", Content: "the answer is 42", ReasoningContent: "a long chain of thought", ReasoningTrimmed: true},
	}
}

// TestDefaultViewIsTheWholeFile pins the compatibility promise: the raw-data
// dialog asks with no view and must keep seeing every entry and every field,
// flags included.
func TestDefaultViewIsTheWholeFile(t *testing.T) {
	stored := storedFixture()
	got, err := viewMessages("", stored)
	if err != nil {
		t.Fatalf("viewMessages: %v", err)
	}
	if len(got) != len(stored) {
		t.Fatalf("entries = %d, want %d — the default view dropped something", len(got), len(stored))
	}
	for i := range stored {
		if got[i].Content != stored[i].Content ||
			got[i].ReasoningContent != stored[i].ReasoningContent ||
			got[i].HeartbeatTrim != stored[i].HeartbeatTrim ||
			got[i].ReasoningTrimmed != stored[i].ReasoningTrimmed {
			t.Fatalf("entry %d was rewritten: %+v", i, got[i].Message)
		}
	}
}

// TestChatViewDropsHeartbeatTrimmedEntries covers the larger of the two
// savings, and the progress case with it — the flag marks silently-ended
// progress turns too, which the name does not say.
func TestChatViewDropsHeartbeatTrimmedEntries(t *testing.T) {
	got, err := viewMessages("chat", storedFixture())
	if err != nil {
		t.Fatalf("viewMessages: %v", err)
	}
	for _, m := range got {
		if m.HeartbeatTrim {
			t.Fatalf("chat view kept a heartbeat_trim entry: %+v", m.Message)
		}
	}
	if len(got) != 7 {
		t.Fatalf("entries = %d, want 7 (10 stored, 3 flagged)", len(got))
	}
}

// TestChatViewKeepsTheWakeMarkers: the wake of a trimmed turn is a user-role
// entry the projection hides by prefix, not by flag. Dropping it here would be
// a second, separate decision — and a `progress` wake is one the user asked to
// keep.
func TestChatViewKeepsTheWakeMarkers(t *testing.T) {
	got, err := viewMessages("chat", storedFixture())
	if err != nil {
		t.Fatalf("viewMessages: %v", err)
	}
	var heartbeat, progress int
	for _, m := range got {
		switch m.Compressed {
		case "[heartbeat at 03:12 — trimmed]":
			heartbeat++
		case "[progress at 09:40 — trimmed]":
			progress++
		}
	}
	if heartbeat != 1 || progress != 1 {
		t.Fatalf("wake markers kept: heartbeat=%d progress=%d, want 1 and 1", heartbeat, progress)
	}
}

// TestChatViewStripsOnlyTrimmedReasoning: the text goes, the entry and its flag
// stay. Blanking live reasoning would silently empty the thinking panel.
func TestChatViewStripsOnlyTrimmedReasoning(t *testing.T) {
	got, err := viewMessages("chat", storedFixture())
	if err != nil {
		t.Fatalf("viewMessages: %v", err)
	}
	var checkedLive, checkedTrimmed bool
	for _, m := range got {
		switch {
		case m.ReasoningTrimmed:
			checkedTrimmed = true
			if m.ReasoningContent != "" {
				t.Fatalf("trimmed reasoning still shipped: %q", m.ReasoningContent)
			}
			if m.Content != "the answer is 42" {
				t.Fatalf("the entry itself must survive, got %q", m.Content)
			}
		case m.ReasoningContent != "":
			checkedLive = true
			if m.ReasoningContent != "live reasoning" {
				t.Fatalf("untrimmed reasoning was rewritten: %q", m.ReasoningContent)
			}
		}
	}
	if !checkedLive || !checkedTrimmed {
		t.Fatalf("fixture no longer exercises both cases (live=%v trimmed=%v)", checkedLive, checkedTrimmed)
	}
}

// TestChatViewTokensMatchTheStoredMessage: `tokens` must mean the same thing in
// both views, or two panels reading the same session disagree about what it
// costs. The saving is bytes on the wire, not a rewritten accounting.
func TestChatViewTokensMatchTheStoredMessage(t *testing.T) {
	stored := storedFixture()
	full, err := viewMessages("", stored)
	if err != nil {
		t.Fatalf("viewMessages: %v", err)
	}
	chat, err := viewMessages("chat", stored)
	if err != nil {
		t.Fatalf("viewMessages: %v", err)
	}
	byContent := map[string]int{}
	for _, m := range full {
		if m.ReasoningTrimmed {
			byContent[m.Content] = m.Tokens
		}
	}
	for _, m := range chat {
		if !m.ReasoningTrimmed {
			continue
		}
		if want := byContent[m.Content]; m.Tokens != want {
			t.Fatalf("tokens = %d in chat view, %d in the full view", m.Tokens, want)
		}
	}
}

// TestUnknownViewIsAnError: a typo must not read as success and quietly ship
// the whole file — the failure mode this parameter exists to remove.
func TestUnknownViewIsAnError(t *testing.T) {
	for _, v := range []string{"raw", "Chat", "full", "1"} {
		if _, err := viewMessages(v, storedFixture()); err == nil {
			t.Fatalf("view=%q was accepted", v)
		}
	}
}
