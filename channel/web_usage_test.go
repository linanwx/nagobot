package channel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread"
)

func decodeStats(t *testing.T, rw *httptest.ResponseRecorder) sessionStatsResponse {
	t.Helper()
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}
	var resp sessionStatsResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// The ring must show the number that fires Tier 2 / Tier 3, which counts the
// system prompt and tool definitions on top of the messages. Counting the
// messages alone is the defect this endpoint used to have.
func TestHandleSessionStats_UsedTokensIsTheTierRequestSize(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:usage")
	const sysTokens, toolTokens = 40000, 9000
	ch.SetContextUsageFn(func(string) (thread.ContextUsage, bool) {
		return thread.ContextUsage{
			ContextWindow:      200000,
			TrimBudget:         174000,
			SystemPromptTokens: sysTokens,
			ToolDefsTokens:     toolTokens,
		}, true
	})

	rw := httptest.NewRecorder()
	ch.handleSessionStats(rw, "web:usage")
	resp := decodeStats(t, rw)

	s, err := session.ReadFile(ch.resolveSessionFile("web:usage", session.SessionFileName))
	if err != nil {
		t.Fatal(err)
	}
	want := thread.RequestTokens(sysTokens, toolTokens, s.Messages)
	if resp.UsedTokens != want {
		t.Fatalf("used_tokens = %d, want thread.RequestTokens = %d", resp.UsedTokens, want)
	}
	if got := resp.SystemPromptTokens + resp.MessageTokens + resp.ToolDefsTokens; got != resp.UsedTokens {
		t.Errorf("parts sum to %d, used_tokens = %d", got, resp.UsedTokens)
	}
	if !resp.IsRuntime {
		t.Error("is_runtime = false with a live thread")
	}
	if wantPct := float64(want) / 200000 * 100; resp.UsagePercent != wantPct {
		t.Errorf("usage_percent = %v, want %v", resp.UsagePercent, wantPct)
	}
	if resp.TrimTriggerPercent != 87 {
		t.Errorf("trim_trigger_percent = %v, want 87", resp.TrimTriggerPercent)
	}
}

// Pressure status reads the full request, so a large system prompt alone can
// put a short conversation past Tier 2.
func TestHandleSessionStats_PressureCountsTheSystemPrompt(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:pressure")
	ch.SetContextUsageFn(func(string) (thread.ContextUsage, bool) {
		return thread.ContextUsage{ContextWindow: 100000, TrimBudget: 73616, SystemPromptTokens: 55000}, true
	})
	rw := httptest.NewRecorder()
	ch.handleSessionStats(rw, "web:pressure")
	if got := decodeStats(t, rw).PressureStatus; got != "warning" {
		t.Fatalf("pressure_status = %q, want warning (55K prompt in a 100K window is past Tier 2 at 50%%)", got)
	}
}

// Compressed messages are counted as sent, not as stored.
func TestHandleSessionStats_CountsCompressedMessagesAsSent(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:comp")
	dir := filepath.Dir(ch.resolveSessionFile("web:comp", session.SessionFileName))
	long := strings.Repeat("lorem ipsum dolor sit amet ", 400)
	line := `{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"web_fetch","arguments":"{}"}}],"timestamp":"2026-04-24T00:00:00Z"}` + "\n" +
		`{"role":"tool","name":"web_fetch","tool_call_id":"c1","content":` + jsonString(long) +
		`,"compressed":"[compressed]","timestamp":"2026-04-24T00:00:00Z"}` + "\n"
	f, err := os.OpenFile(filepath.Join(dir, session.SessionFileName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rw := httptest.NewRecorder()
	ch.handleSessionStats(rw, "web:comp")
	resp := decodeStats(t, rw)
	if resp.CompressedMessages != 1 {
		t.Fatalf("compressed_messages = %d, want 1", resp.CompressedMessages)
	}
	if resp.TokensSaved <= 1000 {
		t.Errorf("tokens_saved = %d, want the ~2.4K body counted out", resp.TokensSaved)
	}
	if resp.MessageTokens != resp.RawMessageTokens-resp.TokensSaved {
		t.Errorf("message_tokens %d != raw %d - saved %d", resp.MessageTokens, resp.RawMessageTokens, resp.TokensSaved)
	}
}

// With no thread loaded the prompt and tool sizes are unknown: report them as
// 0 and say so, with the window and trim line from config.
func TestHandleSessionStats_NoThreadIsPartial(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ch := newTestWebChannelWithSession(t, "web:cold")
	ch.SetContextUsageFn(func(string) (thread.ContextUsage, bool) { return thread.ContextUsage{}, false })

	rw := httptest.NewRecorder()
	ch.handleSessionStats(rw, "web:cold")
	resp := decodeStats(t, rw)
	if resp.IsRuntime {
		t.Fatal("is_runtime = true with no thread")
	}
	if resp.SystemPromptTokens != 0 || resp.ToolDefsTokens != 0 {
		t.Errorf("prompt/tools = %d/%d, want 0/0", resp.SystemPromptTokens, resp.ToolDefsTokens)
	}
	if resp.UsedTokens != resp.MessageTokens {
		t.Errorf("used_tokens = %d, want message_tokens %d", resp.UsedTokens, resp.MessageTokens)
	}
	if resp.ContextWindowTokens <= 0 || resp.TrimTriggerPercent <= resp.Tier3TriggerPercent {
		t.Errorf("window %d, trim %v%% vs tier3 %v%%: want a config window with trim above Tier 3",
			resp.ContextWindowTokens, resp.TrimTriggerPercent, resp.Tier3TriggerPercent)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
