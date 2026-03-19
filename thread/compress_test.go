package thread

import (
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/provider"
)

func makeWakeContent(source, thread, session, delivery, action, msg string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("source: " + source + "\n")
	sb.WriteString("thread: " + thread + "\n")
	sb.WriteString("session: " + session + "\n")
	sb.WriteString("time: \"2026-03-06T10:00:00+08:00 (Thursday, Asia/Shanghai, UTC+08:00)\"\n")
	sb.WriteString("delivery: " + delivery + "\n")
	sb.WriteString("visibility: user-visible\n")
	sb.WriteString("action: " + action + "\n")
	sb.WriteString("---\n\n")
	sb.WriteString(msg)
	return sb.String()
}

func TestCompressTier1_WakeTrim(t *testing.T) {
	// Build 5 wake messages + assistant replies.
	var messages []provider.Message
	for i := 0; i < 5; i++ {
		messages = append(messages,
			provider.Message{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "A user sent a message.", "hello")},
			provider.Message{Role: "assistant", Content: "hi"},
		)
	}

	modified, result := compressTier1(messages, 3)
	if !modified {
		t.Fatal("expected modified=true")
	}

	// Content must never be modified.
	for i, m := range result {
		if m.Content != messages[i].Content {
			t.Errorf("message %d: Content was modified", i)
		}
	}

	// Count wake messages whose Compressed strips redundant fields.
	trimmed, kept := 0, 0
	for _, m := range result {
		if m.Role != "user" || !strings.HasPrefix(m.Content, "---\n") {
			continue
		}
		if m.Compressed == "" {
			kept++ // no compression = still intact (protected)
		} else {
			trimmed++
			// Compressed must not contain thread/session/delivery/action.
			if strings.Contains(m.Compressed, "thread:") {
				t.Error("trimmed Compressed still has thread field")
			}
			if strings.Contains(m.Compressed, "session:") {
				t.Error("trimmed Compressed still has session field")
			}
			if strings.Contains(m.Compressed, "delivery:") {
				t.Error("trimmed Compressed still has delivery field")
			}
			if strings.Contains(m.Compressed, "action:") {
				t.Error("trimmed Compressed still has action field")
			}
			// Must preserve source, time, visibility.
			if !strings.Contains(m.Compressed, "source:") {
				t.Error("trimmed Compressed lost source")
			}
			if !strings.Contains(m.Compressed, "time:") {
				t.Error("trimmed Compressed lost time")
			}
			if !strings.Contains(m.Compressed, "visibility:") {
				t.Error("trimmed Compressed lost visibility")
			}
		}
	}
	// Protection is based on last 3 assistant turns (not last 3 wake messages).
	// With 5 wake+assistant pairs and keepLast=3: messages 0-4 are outside protection,
	// so wake messages at idx 0, 2, 4 get trimmed; wake messages at idx 6, 8 are kept.
	if kept != 2 {
		t.Errorf("expected 2 kept wake messages, got %d", kept)
	}
	if trimmed != 3 {
		t.Errorf("expected 3 trimmed wake messages, got %d", trimmed)
	}
}

func TestCompressTier1_WakeTrimIdempotent(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "hint", "hello")},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "hint", "hello2")},
		{Role: "assistant", Content: "hi2"},
		{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "hint", "hello3")},
		{Role: "assistant", Content: "hi3"},
		{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "hint", "hello4")},
		{Role: "assistant", Content: "hi4"},
	}

	_, result := compressTier1(messages, 3)
	modified2, _ := compressTier1(result, 3)
	if modified2 {
		t.Error("second pass should not modify anything (idempotent)")
	}
}

func makeSkillContent(skillName string) string {
	return "---\nskill: " + skillName + "\ndir: /workspace/skills/" + skillName + "\n---\n\n# " + skillName + " instructions\n\nSome skill content here."
}

func TestCompressTier1_SkillOutdated(t *testing.T) {
	// Same skill loaded twice → first one should be compressed as outdated (no hint).
	messages := []provider.Message{
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "use_skill", Arguments: `{"name":"research"}`}}}},
		{Role: "tool", Name: "use_skill", ToolCallID: "c1", Content: makeSkillContent("research")},
		{Role: "assistant", Content: "doing research..."},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "c2", Type: "function", Function: provider.FunctionCall{Name: "use_skill", Arguments: `{"name":"research"}`}}}},
		{Role: "tool", Name: "use_skill", ToolCallID: "c2", Content: makeSkillContent("research")},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "next2"},
		{Role: "assistant", Content: "ok2"},
		{Role: "user", Content: "next3"},
		{Role: "assistant", Content: "ok3"},
	}

	modified, result := compressTier1(messages, 3)
	if !modified {
		t.Fatal("expected modified=true")
	}

	// First skill result (idx 1) should be compressed (outdated).
	m1 := result[1]
	if m1.Compressed == "" {
		t.Fatal("first skill result should be compressed")
	}
	if !strings.Contains(m1.Compressed, "outdated: true") {
		t.Error("first skill should be marked outdated")
	}
	// Outdated should NOT have reload hint.
	if strings.Contains(m1.Compressed, "use_skill to reload") {
		t.Error("outdated skill should not have reload hint")
	}

	// Second skill result (idx 4) should NOT be compressed (latest load, not expired).
	m4 := result[4]
	if m4.Compressed != "" {
		t.Error("latest skill result should not be compressed")
	}
}

func TestCompressTier1_SkillExpired(t *testing.T) {
	// Single skill load older than 1 hour → should be compressed with reload hint.
	messages := []provider.Message{
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "use_skill", Arguments: `{"name":"fallout"}`}}}},
		{Role: "tool", Name: "use_skill", ToolCallID: "c1", Content: makeSkillContent("fallout"), Timestamp: time.Now().Add(-2 * time.Hour)},
		{Role: "assistant", Content: "playing fallout"},
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "next2"},
		{Role: "assistant", Content: "ok2"},
		{Role: "user", Content: "next3"},
		{Role: "assistant", Content: "ok3"},
	}

	modified, result := compressTier1(messages, 3)
	if !modified {
		t.Fatal("expected modified=true for expired skill")
	}

	m := result[1]
	if m.Compressed == "" {
		t.Fatal("expired skill should be compressed")
	}
	if !strings.Contains(m.Compressed, "outdated: true") {
		t.Error("expired skill should be marked outdated")
	}
	// Expired SHOULD have reload hint.
	if !strings.Contains(m.Compressed, "use_skill to reload") {
		t.Error("expired skill should have reload hint")
	}
}

func TestCompressTier1_SkillNotExpired(t *testing.T) {
	// Single skill load within 1 hour → should NOT be compressed.
	messages := []provider.Message{
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "use_skill", Arguments: `{"name":"research"}`}}}},
		{Role: "tool", Name: "use_skill", ToolCallID: "c1", Content: makeSkillContent("research"), Timestamp: time.Now().Add(-30 * time.Minute)},
		{Role: "assistant", Content: "researching"},
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "next2"},
		{Role: "assistant", Content: "ok2"},
		{Role: "user", Content: "next3"},
		{Role: "assistant", Content: "ok3"},
	}

	modified, result := compressTier1(messages, 3)
	// May be modified due to wake trim, but skill should not be compressed.
	m := result[1]
	if m.Compressed != "" {
		t.Errorf("skill within 1 hour should not be compressed, got: %s", m.Compressed)
	}
	_ = modified
}

func TestCompressTier1_HeartbeatExpired(t *testing.T) {
	// Heartbeat source + >6h + >100 bytes → header-only
	messages := []provider.Message{
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "exec", Arguments: `{"cmd":"nagobot list-sessions"}`}}}},
		{Role: "tool", Name: "exec", ToolCallID: "c1", Content: strings.Repeat("session data\n", 50), Source: "heartbeat", Timestamp: time.Now().Add(-8 * time.Hour)},
		{Role: "assistant", Content: "HEARTBEAT_OK", Source: "heartbeat"},
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "next2"},
		{Role: "assistant", Content: "ok2"},
		{Role: "user", Content: "next3"},
		{Role: "assistant", Content: "ok3"},
	}

	modified, result := compressTier1(messages, 3)
	if !modified {
		t.Fatal("expected modified=true")
	}

	m := result[1]
	if m.Compressed == "" {
		t.Fatal("heartbeat tool result >6h should be compressed")
	}
	if !strings.Contains(m.Compressed, "compressed: exec") {
		t.Error("should have compressed header with tool name")
	}
	// Should be header-only (no body content from original).
	if strings.Contains(m.Compressed, "session data") {
		t.Error("header-only compression should not contain original body")
	}
}

func TestCompressTier1_HeartbeatRecent(t *testing.T) {
	// Heartbeat source + <2h → not compressed
	messages := []provider.Message{
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "exec", Arguments: `{"cmd":"nagobot list-sessions"}`}}}},
		{Role: "tool", Name: "exec", ToolCallID: "c1", Content: strings.Repeat("session data\n", 50), Source: "heartbeat", Timestamp: time.Now().Add(-1 * time.Hour)},
		{Role: "assistant", Content: "HEARTBEAT_OK", Source: "heartbeat"},
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "next2"},
		{Role: "assistant", Content: "ok2"},
		{Role: "user", Content: "next3"},
		{Role: "assistant", Content: "ok3"},
	}

	_, result := compressTier1(messages, 3)
	m := result[1]
	// Content is 650 bytes, above 3000 threshold? No, 13*50=650 < 3000. So no compression at all.
	if m.Compressed != "" {
		t.Errorf("heartbeat tool result <2h should not be compressed, got: %s", m.Compressed)
	}
}

func TestCompressTier1_HeartbeatSmall(t *testing.T) {
	// Heartbeat source + >2h + ≤100 bytes → not compressed
	messages := []provider.Message{
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "sleep_thread", Arguments: `{"skip":true}`}}}},
		{Role: "tool", Name: "sleep_thread", ToolCallID: "c1", Content: "ok: skipped", Source: "heartbeat", Timestamp: time.Now().Add(-8 * time.Hour)},
		{Role: "assistant", Content: "HEARTBEAT_OK", Source: "heartbeat"},
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "next2"},
		{Role: "assistant", Content: "ok2"},
		{Role: "user", Content: "next3"},
		{Role: "assistant", Content: "ok3"},
	}

	_, result := compressTier1(messages, 3)
	m := result[1]
	if m.Compressed != "" {
		t.Errorf("small heartbeat tool result should not be compressed, got: %s", m.Compressed)
	}
}

func TestCompressTier1_WakeBodyTrimSkipSmall(t *testing.T) {
	// Wake body barely above 3000 → 95% guard should skip body trim, only YAML trim.
	body := strings.Repeat("x", 3064)
	content := "---\nsource: child_completed\nthread: t-1\nsession: s-1\ndelivery: d-1\ntime: \"2026-03-06T10:00:00+08:00\"\nvisibility: assistant-only\naction: hint\n---\n" + body

	messages := []provider.Message{
		{Role: "user", Content: content},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "ok2"},
		{Role: "user", Content: "next2"},
		{Role: "assistant", Content: "ok3"},
		{Role: "user", Content: "next3"},
		{Role: "assistant", Content: "ok4"},
	}

	modified, result := compressTier1(messages, 3)
	if !modified {
		t.Fatal("expected modified=true for YAML trim")
	}

	m := result[0]
	if m.Compressed == "" {
		t.Fatal("should at least have YAML field trim")
	}
	// Should NOT have body compression (95% guard).
	if strings.Contains(m.Compressed, "compressed: true") {
		t.Error("body trim should be skipped when result is not 5% smaller")
	}
	// Should still trim redundant YAML fields.
	if strings.Contains(m.Compressed, "thread:") {
		t.Error("should still strip thread field")
	}
	// Body should be fully preserved.
	if !strings.Contains(m.Compressed, body) {
		t.Error("body should be preserved when body trim is skipped")
	}
}

func TestCompressTier1_ReasoningTrimmed(t *testing.T) {
	// Assistant message >3h old with reasoning → should be marked ReasoningTrimmed.
	messages := []provider.Message{
		{Role: "assistant", Content: "answer", ReasoningContent: "thinking...", Timestamp: time.Now().Add(-4 * time.Hour)},
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "next2"},
		{Role: "assistant", Content: "ok2"},
		{Role: "user", Content: "next3"},
		{Role: "assistant", Content: "ok3"},
	}

	modified, result := compressTier1(messages, 3)
	if !modified {
		t.Fatal("expected modified=true")
	}

	m := result[0]
	if !m.ReasoningTrimmed {
		t.Error("old assistant reasoning should be marked trimmed")
	}
	// Original data must be preserved.
	if m.ReasoningContent != "thinking..." {
		t.Error("ReasoningContent should be preserved (not cleared)")
	}
}

func TestCompressTier1_ReasoningNotTrimmedRecent(t *testing.T) {
	// Assistant message <3h old with reasoning → should NOT be marked.
	messages := []provider.Message{
		{Role: "assistant", Content: "answer", ReasoningContent: "thinking...", Timestamp: time.Now().Add(-1 * time.Hour)},
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "next2"},
		{Role: "assistant", Content: "ok2"},
		{Role: "user", Content: "next3"},
		{Role: "assistant", Content: "ok3"},
	}

	_, result := compressTier1(messages, 3)
	if result[0].ReasoningTrimmed {
		t.Error("recent assistant reasoning should not be trimmed")
	}
}

func TestCompressTier1_ReasoningProtectedByBoundary(t *testing.T) {
	// Assistant within protectFrom (last 3 assistant turns) should NOT be marked
	// even if old.
	messages := []provider.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1", ReasoningContent: "r1", Timestamp: time.Now().Add(-5 * time.Hour)},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2", ReasoningContent: "r2", Timestamp: time.Now().Add(-4 * time.Hour)},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "a3", ReasoningContent: "r3", Timestamp: time.Now().Add(-4 * time.Hour)},
	}

	_, result := compressTier1(messages, 3)
	// All 3 assistants are within protectFrom (only 3 assistant turns total).
	for i, m := range result {
		if m.Role == "assistant" && m.ReasoningTrimmed {
			t.Errorf("message %d: protected assistant should not be trimmed", i)
		}
	}
}

func TestSoftTrimWithHint_SkipWhenNotSmallEnough(t *testing.T) {
	// Content barely above threshold (3064 chars) → overhead makes result larger → should skip.
	content := strings.Repeat("x", 3064)
	result := softTrimWithHint(content, "web_fetch", "msg-123")
	if result != "" {
		t.Errorf("soft trim should skip when result is not at least 5%% smaller, got len=%d (original=%d)", len(result), len(content))
	}

	// Content well above threshold (10000 chars) → should compress.
	bigContent := strings.Repeat("y", 10000)
	result2 := softTrimWithHint(bigContent, "web_fetch", "msg-456")
	if result2 == "" {
		t.Error("soft trim should compress large content")
	}
	if len(result2) >= int(float64(len(bigContent))*0.95) {
		t.Errorf("compressed result should be at least 5%% smaller: got %d, original %d", len(result2), len(bigContent))
	}
}
