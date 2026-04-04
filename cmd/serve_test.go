package cmd

import (
	"testing"

	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread"
)

func TestBuildDefaultAgentFor_NeverEmpty(t *testing.T) {
	tests := []struct {
		name       string
		sessionKey string
		agent      string // persisted in meta.json; "" means no meta
		want       string
	}{
		{
			name:       "meta.json has agent",
			sessionKey: "telegram:123",
			agent:      "fallout",
			want:       "fallout",
		},
		{
			name:       "meta.json has no agent",
			sessionKey: "telegram:123",
			agent:      "",
			want:       "soul",
		},
		{
			name:       "no meta.json at all",
			sessionKey: "telegram:456",
			agent:      "", // won't write meta.json
			want:       "soul",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionsDir := t.TempDir()
			sessMgr, err := session.NewManager(sessionsDir)
			if err != nil {
				t.Fatal(err)
			}
			mgr := thread.NewManager(&thread.ThreadConfig{
				Sessions: sessMgr,
			})

			if tt.agent != "" {
				dir := session.SessionDir(sessionsDir, tt.sessionKey)
				session.UpdateMeta(dir, func(m *session.Meta) {
					m.Agent = tt.agent
				})
			}

			fn := buildDefaultAgentFor(mgr)
			got := fn(tt.sessionKey)
			if got == "" {
				t.Fatalf("buildDefaultAgentFor returned empty string for %q", tt.sessionKey)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
