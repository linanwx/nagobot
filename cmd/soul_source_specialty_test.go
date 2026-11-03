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
		string(msg.WakeHeartbeat): {"toolcall"},
		string(msg.WakeCron):      {"cron"},
	}
	for src, specs := range want {
		if got := []string(meta.SourceSpecialties[src]); !reflect.DeepEqual(got, specs) {
			t.Errorf("soul source_specialty[%q] = %v, want %v", src, got, specs)
		}
	}
}
