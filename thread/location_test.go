package thread

import (
	"path/filepath"
	"testing"

	"github.com/linanwx/nagobot/session"
)

// TestLocationForPrecedence pins the wake-time zone resolution:
// client-reported (meta.json) > server config > server system zone.
func TestLocationForPrecedence(t *testing.T) {
	store, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "web:tz-test"
	// PathForKey returns the session.jsonl path; meta.json lives in its dir,
	// which is exactly what Manager.SessionDir resolves.
	sessDir := filepath.Dir(store.PathForKey(key))

	configZone := ""
	mgr := NewManager(&ThreadConfig{
		Sessions:           store,
		SessionTimezoneFor: func(string) string { return configZone },
	})

	// 1. Nothing set → server system zone (whatever the test host runs in).
	if got := mgr.locationFor(key).String(); got == "" {
		t.Fatalf("empty zone name for default case")
	}

	// 2. Only server config set → that zone.
	configZone = "America/New_York"
	if got := mgr.locationFor(key).String(); got != "America/New_York" {
		t.Fatalf("config zone: got %q, want America/New_York", got)
	}

	// 3. Client zone in meta.json OUTRANKS the config zone.
	session.UpdateMeta(sessDir, func(m *session.Meta) { m.ClientTimezone = "Asia/Shanghai" })
	if got := mgr.locationFor(key).String(); got != "Asia/Shanghai" {
		t.Fatalf("client zone must win over config: got %q, want Asia/Shanghai", got)
	}

	// 4. A malformed client zone falls through to the config zone, not an error.
	session.UpdateMeta(sessDir, func(m *session.Meta) { m.ClientTimezone = "Not/AZone" })
	if got := mgr.locationFor(key).String(); got != "America/New_York" {
		t.Fatalf("invalid client zone must fall through to config: got %q", got)
	}
}
