package cmd

import (
	"reflect"
	"testing"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/thread/msg"
)

// TestSoulRoutesCronTurnsThroughCronSpecialty pins the shipped soul template's
// per-source routing. A scheduled job fired into a chat session re-reads the
// whole conversation, usually long after its cache expired, so it is the
// most expensive turn a chat agent runs; mapping the cron source to the
// `cron` specialty lets a deployment send it to a cheaper model with one
// rule. Without a `cron` rule the resolution cascades to `chat`, so
// deployments that never configured one keep today's routing.
//
// progress and compression ride `toolcall` for the same reason heartbeat does:
// neither speaks to the human directly, both re-read the whole conversation
// (measured on kingsley the week to 2026-11-23: progress 36% and compression
// 15% of the chat agent's subscription credits), so a deployment can put the
// conversation on a stronger model without paying its rate for them.
func TestSoulRoutesCronTurnsThroughCronSpecialty(t *testing.T) {
	data, err := templateFS.ReadFile("templates/agents/soul.md")
	if err != nil {
		t.Fatal(err)
	}
	meta, _, _, err := agent.ParseTemplate(string(data))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		string(msg.WakeHeartbeat):   {"toolcall"},
		string(msg.WakeCron):        {"cron"},
		string(msg.WakeProgress):    {"toolcall"},
		string(msg.WakeCompression): {"toolcall"},
	}
	for src, specs := range want {
		if got := []string(meta.SourceSpecialties[src]); !reflect.DeepEqual(got, specs) {
			t.Errorf("soul source_specialty[%q] = %v, want %v", src, got, specs)
		}
	}
}
