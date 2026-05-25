package cmd

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread"
)

type recordingChannel struct {
	name string
	sent []channel.Response
}

func (c *recordingChannel) Name() string { return c.name }

func (c *recordingChannel) Start(context.Context) error { return nil }

func (c *recordingChannel) Stop() error { return nil }

func (c *recordingChannel) Send(_ context.Context, resp *channel.Response) error {
	c.sent = append(c.sent, *resp)
	return nil
}

func (c *recordingChannel) Messages() <-chan *channel.Message { return nil }

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

func TestBuildDefaultSinkFor_DiscordInvalidSessionKeysAreSilent(t *testing.T) {
	tests := []struct {
		name       string
		sessionKey string
	}{
		{
			name:       "prethink sibling",
			sessionKey: "discord:1502707848944287895:prethink",
		},
		{
			name:       "non snowflake",
			sessionKey: "discord:not-a-snowflake",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chMgr := channel.NewManager()
			discordCh := &recordingChannel{name: "discord"}
			chMgr.Register(discordCh)

			fn := buildDefaultSinkFor(chMgr, nil, t.TempDir(), nil, nil)
			sink := fn(tt.sessionKey)
			if sink.IsZero() {
				t.Fatal("expected a silent sink, got zero sink")
			}
			if !strings.Contains(sink.Label, "internal") {
				t.Errorf("label %q should mark the session as internal/non-deliverable", sink.Label)
			}

			if err := sink.Send(context.Background(), "assistant output"); err != nil {
				t.Fatalf("silent sink returned error: %v", err)
			}
			if len(discordCh.sent) != 0 {
				t.Fatalf("invalid discord session should not send to channel, sent %d message(s): %+v", len(discordCh.sent), discordCh.sent)
			}
		})
	}
}

func TestBuildDefaultSinkFor_DiscordSnowflakeSendsToChannel(t *testing.T) {
	chMgr := channel.NewManager()
	discordCh := &recordingChannel{name: "discord"}
	chMgr.Register(discordCh)

	fn := buildDefaultSinkFor(chMgr, nil, t.TempDir(), nil, nil)
	sink := fn("discord:1502707848944287895")
	if sink.IsZero() {
		t.Fatal("expected discord sink")
	}

	if err := sink.Send(context.Background(), "hello"); err != nil {
		t.Fatalf("discord sink returned error: %v", err)
	}
	if len(discordCh.sent) != 1 {
		t.Fatalf("expected 1 discord send, got %d", len(discordCh.sent))
	}
	if got := discordCh.sent[0].ReplyTo; got != "1502707848944287895" {
		t.Fatalf("replyTo = %q, want channel snowflake", got)
	}
}

func TestInstallShutdownHandlerForcesExitOnSecondSignal(t *testing.T) {
	sigCh := make(chan os.Signal, 2)
	shutdownCh := make(chan struct{})
	cancelled := make(chan struct{})
	exitCode := make(chan int, 1)

	installShutdownHandler(sigCh, shutdownCh, func() {
		close(cancelled)
	}, func(code int) {
		exitCode <- code
	}, time.Hour)

	sigCh <- syscall.SIGTERM
	select {
	case <-cancelled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("shutdown handler did not call cancel after first signal")
	}

	sigCh <- syscall.SIGTERM
	select {
	case got := <-exitCode:
		if got != 1 {
			t.Fatalf("exit code = %d, want 1", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("shutdown handler did not force exit after second signal")
	}
}

func TestInstallShutdownHandlerForcesExitAfterTimeout(t *testing.T) {
	sigCh := make(chan os.Signal, 1)
	shutdownCh := make(chan struct{})
	cancelled := make(chan struct{})
	exitCode := make(chan int, 1)

	installShutdownHandler(sigCh, shutdownCh, func() {
		close(cancelled)
	}, func(code int) {
		exitCode <- code
	}, 10*time.Millisecond)

	close(shutdownCh)
	select {
	case <-cancelled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("shutdown handler did not call cancel after RPC shutdown")
	}

	select {
	case got := <-exitCode:
		if got != 1 {
			t.Fatalf("exit code = %d, want 1", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("shutdown handler did not force exit after timeout")
	}
}
