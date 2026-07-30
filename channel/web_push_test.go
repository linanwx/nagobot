package channel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A notification headline must never be a bare session key: "web:2118acc7"
// tells the person holding the phone nothing about what just arrived.
func TestPushTitlePrefersSummaryAndNeverShowsTheKey(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:abc")
	sysDir := filepath.Join(ch.workspace, "system")
	if err := os.MkdirAll(sysDir, 0o755); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("摘要", 40)
	body := `{
	  "web:abc": {"summary": "Dublin weather cron\nand clothing advice"},
	  "web:long": {"summary": "` + long + `"},
	  "web:blank": {"summary": ""}
	}`
	if err := os.WriteFile(filepath.Join(sysDir, "sessions_summary.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// A summary is free prose: a newline in it must not reach the title.
	if got := ch.pushTitle("web:abc"); got != "Dublin weather cron and clothi…" {
		t.Errorf("summary title = %q", got)
	}
	if got := ch.pushTitle("web:long"); len([]rune(got)) != pushTitleRunes+1 {
		t.Errorf("long title = %q (%d runes), want %d + ellipsis", got, len([]rune(got)), pushTitleRunes)
	}
	for _, key := range []string{"web:blank", "web:missing"} {
		if got := ch.pushTitle(key); got != "nagobot" {
			t.Errorf("pushTitle(%q) = %q, want the product name", key, got)
		}
	}
	// The whole point: no branch may leak the key.
	for _, key := range []string{"web:abc", "web:long", "web:blank", "web:missing"} {
		if strings.Contains(ch.pushTitle(key), key) {
			t.Errorf("pushTitle(%q) leaked the session key", key)
		}
	}
}
