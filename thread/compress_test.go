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
	sb.WriteString("sender: user\n")
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
			// Must preserve source, time, sender.
			if !strings.Contains(m.Compressed, "source:") {
				t.Error("trimmed Compressed lost source")
			}
			if !strings.Contains(m.Compressed, "time:") {
				t.Error("trimmed Compressed lost time")
			}
			if !strings.Contains(m.Compressed, "sender:") {
				t.Error("trimmed Compressed lost sender")
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

func TestCompressTier1_HeartbeatSmallContent(t *testing.T) {
	// Heartbeat tool results with small content are not compressed (normal softTrim threshold applies).
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
	// Content is 650 chars, below softTrim threshold (3000) — no compression.
	if modified {
		t.Fatal("expected modified=false for small heartbeat tool result")
	}
	if result[1].Compressed != "" {
		t.Fatal("small heartbeat tool result should not be compressed")
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
	content := "---\nsource: child_completed\nthread: t-1\nsession: s-1\ndelivery: d-1\ntime: \"2026-03-06T10:00:00+08:00\"\nsender: system\naction: hint\n---\n" + body

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

func TestRuneHeadTail(t *testing.T) {
	// ASCII: runeHead/runeTail should behave like byte slicing.
	s := "abcdefghij"
	if got := runeHead(s, 5); got != "abcde" {
		t.Errorf("runeHead ASCII: got %q, want %q", got, "abcde")
	}
	if got := runeTail(s, 5); got != "fghij" {
		t.Errorf("runeTail ASCII: got %q, want %q", got, "fghij")
	}

	// CJK: each character is 3 bytes. runeHead/runeTail must not split them.
	cjk := "你好世界测试一二三四" // 10 CJK runes, 30 bytes
	if got := runeHead(cjk, 5); got != "你好世界测" {
		t.Errorf("runeHead CJK: got %q, want %q", got, "你好世界测")
	}
	if got := runeTail(cjk, 5); got != "试一二三四" {
		t.Errorf("runeTail CJK: got %q, want %q", got, "试一二三四")
	}

	// Edge: n >= len(runes) returns original string.
	if got := runeHead(cjk, 100); got != cjk {
		t.Errorf("runeHead overflow: got %q, want %q", got, cjk)
	}
	if got := runeTail(cjk, 100); got != cjk {
		t.Errorf("runeTail overflow: got %q, want %q", got, cjk)
	}
}

func TestSoftTrimWithHint_CJK(t *testing.T) {
	// Build content with CJK characters well above threshold.
	// Each CJK char = 1 rune = 3 bytes. 5000 runes = 15000 bytes.
	cjkContent := strings.Repeat("中", 5000)
	result := softTrimWithHint(cjkContent, "web_fetch", "msg-cjk")
	if result == "" {
		t.Fatal("soft trim should compress large CJK content")
	}

	// Verify head and tail are valid UTF-8 by checking they contain the expected CJK chars.
	// Head should be exactly softTrimHeadRunes CJK chars.
	expectedHead := strings.Repeat("中", softTrimHeadRunes)
	if !strings.Contains(result, expectedHead) {
		t.Error("compressed result should contain head with exactly softTrimHeadRunes CJK chars")
	}
	// Tail should be exactly softTrimTailRunes CJK chars.
	expectedTail := strings.Repeat("中", softTrimTailRunes)
	if !strings.Contains(result, expectedTail) {
		t.Error("compressed result should contain tail with exactly softTrimTailRunes CJK chars")
	}
}

func TestComputeWakeCompressed_CJK(t *testing.T) {
	// Build a wake message with a large CJK body that should trigger body compression.
	// Need body > softTrimHeadRunes + softTrimTailRunes runes and sender: system.
	body := strings.Repeat("汉", 5000) // 5000 CJK runes
	content := "---\nsource: child_completed\nthread: t-1\nsession: s-1\ndelivery: d-1\ntime: \"2026-03-06T10:00:00+08:00\"\nsender: system\naction: hint\n---\n" + body

	m := &provider.Message{Content: content, ID: "msg-wake-cjk"}
	result := computeWakeCompressed(m)
	if result == "" {
		t.Fatal("expected compression for large CJK wake body")
	}
	if !strings.Contains(result, "compressed: true") {
		t.Fatal("expected body compression marker")
	}

	// Verify the compressed body contains valid CJK head and tail.
	expectedHead := strings.Repeat("汉", softTrimHeadRunes)
	if !strings.Contains(result, expectedHead) {
		t.Error("compressed wake body should contain head with softTrimHeadRunes CJK chars")
	}
	expectedTail := strings.Repeat("汉", softTrimTailRunes)
	if !strings.Contains(result, expectedTail) {
		t.Error("compressed wake body should contain tail with softTrimTailRunes CJK chars")
	}
}

func TestIsHeartbeatSkipTurn_SleepThread(t *testing.T) {
	// Classic case: sleep_thread called → should trim.
	msgs := []provider.Message{
		{Role: "user", Content: "heartbeat wake", Source: "heartbeat"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "use_skill", Arguments: `{"name":"heartbeat-wake"}`}}}},
		{Role: "tool", Name: "use_skill", ToolCallID: "c1", Content: "skill loaded"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "c2", Type: "function", Function: provider.FunctionCall{Name: "sleep_thread", Arguments: `{}`}}}},
		{Role: "tool", Name: "sleep_thread", ToolCallID: "c2", Content: "ok"},
	}
	if !isHeartbeatSkipTurn(msgs) {
		t.Error("turn with sleep_thread + safe tools should be trimmed")
	}
}

func TestIsHeartbeatSkipTurn_SleepMarkerNoTools(t *testing.T) {
	// SLEEP_THREAD_OK marker with no tool calls → should trim.
	msgs := []provider.Message{
		{Role: "user", Content: "heartbeat wake", Source: "heartbeat"},
		{Role: "assistant", Content: "Nothing to do. SLEEP_THREAD_OK"},
	}
	if !isHeartbeatSkipTurn(msgs) {
		t.Error("turn with SLEEP_THREAD_OK marker and no tool calls should be trimmed")
	}
}

func TestIsHeartbeatSkipTurn_SleepMarkerEmbeddedText(t *testing.T) {
	// SLEEP_THREAD_OK embedded in longer text → should still trim.
	msgs := []provider.Message{
		{Role: "user", Content: "heartbeat wake", Source: "heartbeat"},
		{Role: "assistant", Content: "I reviewed the heartbeat items. Nothing relevant right now.\n\nSLEEP_THREAD_OK\n\nWill check again later."},
	}
	if !isHeartbeatSkipTurn(msgs) {
		t.Error("SLEEP_THREAD_OK embedded in text should still trigger trim")
	}
}

func TestIsHeartbeatSkipTurn_SleepMarkerWithToolCalls(t *testing.T) {
	// SLEEP_THREAD_OK but model also made tool calls → should NOT trim (ambiguous).
	msgs := []provider.Message{
		{Role: "user", Content: "heartbeat wake", Source: "heartbeat"},
		{Role: "assistant", Content: "SLEEP_THREAD_OK", ToolCalls: []provider.ToolCall{{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "exec", Arguments: `{"cmd":"echo hi"}`}}}},
		{Role: "tool", Name: "exec", ToolCallID: "c1", Content: "hi"},
	}
	if isHeartbeatSkipTurn(msgs) {
		t.Error("turn with SLEEP_THREAD_OK but also tool calls should NOT be trimmed")
	}
}

func TestIsHeartbeatSkipTurn_NoSleepNoMarker(t *testing.T) {
	// Neither sleep_thread nor SLEEP_THREAD_OK → should NOT trim (real response).
	msgs := []provider.Message{
		{Role: "user", Content: "heartbeat wake", Source: "heartbeat"},
		{Role: "assistant", Content: "Hey! Just wanted to let you know the weather looks great today."},
	}
	if isHeartbeatSkipTurn(msgs) {
		t.Error("turn without sleep_thread or SLEEP_THREAD_OK should NOT be trimmed")
	}
}

func TestIsHeartbeatSkipTurn_SleepWithExec(t *testing.T) {
	// sleep_thread called + exec ran external command → still trim.
	// If the AI chose silence, the turn is noise regardless of tools used.
	msgs := []provider.Message{
		{Role: "user", Content: "heartbeat wake", Source: "heartbeat"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
			{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "exec", Arguments: `{"command":"curl https://example.com"}`}},
		}},
		{Role: "tool", Name: "exec", ToolCallID: "c1", Content: "ok"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
			{ID: "c2", Type: "function", Function: provider.FunctionCall{Name: "sleep_thread", Arguments: `{}`}},
		}},
		{Role: "tool", Name: "sleep_thread", ToolCallID: "c2", Content: "ok"},
	}
	if !isHeartbeatSkipTurn(msgs) {
		t.Error("turn with sleep_thread should be trimmed regardless of other tools")
	}
}

func TestRuneLen(t *testing.T) {
	if got := runeLen("hello"); got != 5 {
		t.Errorf("runeLen ASCII: got %d, want 5", got)
	}
	if got := runeLen("你好"); got != 2 {
		t.Errorf("runeLen CJK: got %d, want 2", got)
	}
	if got := runeLen(""); got != 0 {
		t.Errorf("runeLen empty: got %d, want 0", got)
	}
}

func TestCompressTier1_SkipsMessageWithSkipTrim(t *testing.T) {
	// Messages with SkipTrim=true should never be compressed, regardless of size or age.
	content := "---\ntool: exec\nstatus: ok\nskip_trim: true\n---\n\n" +
		strings.Repeat("这是压缩摘要内容。", 500)

	messages := []provider.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
			{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "exec", Arguments: "{}"}},
		}},
		{Role: "tool", ToolCallID: "tc1", Name: "exec", Content: content, SkipTrim: true, Timestamp: time.Now().Add(-4 * time.Hour)},
		{Role: "assistant", Content: "COMPRESS_OK"},
	}

	_, result := compressTier1(messages, 3)
	if result[2].Compressed != "" {
		t.Errorf("SkipTrim message should not be compressed, got: %s", result[2].Compressed[:100])
	}
}

func TestCompressTier1_DoesNotFalsePositiveSkipTrim(t *testing.T) {
	// Content containing "skip_trim: true" but without SkipTrim field set
	// must still be compressed normally (e.g. read_file of source code).
	body := strings.Repeat("some code\n", 100) +
		"\nskip_trim: true\n" +
		strings.Repeat("more code\n", 100)
	content := "---\ntool: read_file\nstatus: ok\n---\n\n" + body

	messages := []provider.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
			{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read_file", Arguments: "{}"}},
		}},
		// SkipTrim NOT set — field check only, not content scan
		{Role: "tool", ToolCallID: "tc1", Name: "read_file", Content: content, Timestamp: time.Now().Add(-4 * time.Hour)},
		{Role: "assistant", Content: "done"},
	}

	_, result := compressTier1(messages, 3)
	if result[2].Compressed == "" {
		t.Error("read_file with skip_trim in body should still be compressed (SkipTrim field not set)")
	}
}

func TestApplySlideWindow(t *testing.T) {
	// Plain two-message turns: u-a-u-a-...
	turn := func(i int) []provider.Message {
		return []provider.Message{
			{Role: "user", Content: "user " + string(rune('0'+i))},
			{Role: "assistant", Content: "assistant " + string(rune('0'+i))},
		}
	}
	var ten []provider.Message
	for i := 0; i < 10; i++ {
		ten = append(ten, turn(i)...)
	}

	// Empty input and zero/negative keep → unchanged.
	if got := applySlideWindow(nil, 5); got != nil {
		t.Errorf("nil input: got %v", got)
	}
	if got := applySlideWindow(ten, 0); len(got) != len(ten) {
		t.Errorf("keep=0: expected unchanged, got len=%d", len(got))
	}
	if got := applySlideWindow(ten, -1); len(got) != len(ten) {
		t.Errorf("keep=-1: expected unchanged, got len=%d", len(got))
	}

	// Fewer turns than keep → unchanged.
	two := append([]provider.Message{}, turn(0)...)
	two = append(two, turn(1)...)
	if got := applySlideWindow(two, 5); len(got) != 4 {
		t.Errorf("2 turns with keep=5: expected 4 messages, got %d", len(got))
	}

	// Exactly keep turns → unchanged (cut index lands at 0).
	if got := applySlideWindow(ten, 10); len(got) != 20 {
		t.Errorf("10 turns with keep=10: expected 20 messages, got %d", len(got))
	}

	// Trim: 10 turns, keep 5 → last 10 messages.
	got := applySlideWindow(ten, 5)
	if len(got) != 10 {
		t.Fatalf("10 turns with keep=5: expected 10 messages, got %d", len(got))
	}
	if got[0].Content != "user 5" {
		t.Errorf("expected first retained message to be 'user 5', got %q", got[0].Content)
	}
	if got[len(got)-1].Content != "assistant 9" {
		t.Errorf("expected last retained message to be 'assistant 9', got %q", got[len(got)-1].Content)
	}
}

func TestApplySlideWindow_PreservesToolCallPairs(t *testing.T) {
	// A turn with tool calls: user → assistant(tc) → tool → tool → assistant(final)
	messages := []provider.Message{
		// Turn 1 (plain)
		{Role: "user", Content: "t1 user"},
		{Role: "assistant", Content: "t1 reply"},
		// Turn 2 (with tools)
		{Role: "user", Content: "t2 user"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "c1"}, {ID: "c2"}}},
		{Role: "tool", ToolCallID: "c1", Content: "r1"},
		{Role: "tool", ToolCallID: "c2", Content: "r2"},
		{Role: "assistant", Content: "t2 final"},
		// Turn 3 (plain)
		{Role: "user", Content: "t3 user"},
		{Role: "assistant", Content: "t3 reply"},
	}

	// Keep last 2 turns: drop turn 1.
	got := applySlideWindow(messages, 2)
	if len(got) != 7 {
		t.Fatalf("expected 7 messages after trim, got %d", len(got))
	}
	if got[0].Content != "t2 user" {
		t.Errorf("cut should land at turn 2 user, got %q", got[0].Content)
	}
	// Verify tool_call pair wasn't split.
	if len(got[1].ToolCalls) != 2 || got[2].Role != "tool" || got[3].Role != "tool" {
		t.Errorf("tool call pair was split: %+v", got[1:4])
	}
}

func TestApplySlideWindow_SkipsInjectedUserMessages(t *testing.T) {
	injectedContent := "---\nsource: telegram\nthread: t1\nsession: s1\ndelivery: d\nsender: user\ninjected: true\n---\n\nmid-turn message"
	normalContent := func(body string) string {
		return "---\nsource: telegram\nthread: t1\nsession: s1\ndelivery: d\nsender: user\n---\n\n" + body
	}

	messages := []provider.Message{
		{Role: "user", Content: normalContent("turn 1")},
		{Role: "assistant", Content: "reply 1"},
		{Role: "user", Content: normalContent("turn 2")},
		{Role: "user", Content: injectedContent}, // mid-turn injected, NOT a new turn boundary
		{Role: "assistant", Content: "reply 2"},
		{Role: "user", Content: normalContent("turn 3")},
		{Role: "assistant", Content: "reply 3"},
	}

	// Keep last 2 turns: drop turn 1 only. Injected should not count.
	got := applySlideWindow(messages, 2)
	if len(got) != 5 {
		t.Fatalf("expected 5 messages after trim (turns 2+3 incl injected), got %d", len(got))
	}
	// First kept message should be "turn 2" (not the injected one).
	if !strings.Contains(got[0].Content, "turn 2") {
		t.Errorf("expected first kept message to be turn 2, got: %q", got[0].Content)
	}
}
