package channel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cronpkg "github.com/linanwx/nagobot/cron"
)

func TestHandleSessionCron_ListsOnlyThisSessionsJobsSoonestFirst(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "telegram:1")
	soon := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	ch.SetCronJobsFn(func() []cronpkg.Job {
		return []cronpkg.Job{
			// Inject into this session: runs here.
			{ID: "nudge", Kind: cronpkg.JobKindCron, Expr: "0 8 * * *", Task: "Good morning", WakeSession: "telegram:1", DirectWake: true},
			// Independent, reports here.
			{ID: "brief", Kind: cronpkg.JobKindCron, Expr: "not a cron expr", Task: "Brief", Agent: "general", WakeSession: "telegram:1"},
			// One-time inject, fires within the hour: must sort first.
			{ID: "followup", Kind: cronpkg.JobKindAt, AtTime: &soon, Task: "Check in", WakeSession: "telegram:1", DirectWake: true},
			// Another session's job: must not appear.
			{ID: "other", Kind: cronpkg.JobKindCron, Expr: "0 9 * * *", Task: "x", WakeSession: "discord:2", DirectWake: true},
		}
	})

	rw := httptest.NewRecorder()
	ch.handleSessionMessages(rw, httptest.NewRequest(http.MethodGet, "/api/sessions/telegram/1/cron", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rw.Code, rw.Body.String())
	}
	var resp sessionCronResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Key != "telegram:1" || resp.Timezone == "" {
		t.Errorf("key=%q timezone=%q", resp.Key, resp.Timezone)
	}
	var ids []string
	for _, j := range resp.Jobs {
		ids = append(ids, j.ID)
	}
	// followup (≤1h) < nudge (next 08:00, ≤24h) < brief (unparseable, last).
	want := []string{"followup", "nudge", "brief"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
	byID := map[string]sessionCronJob{}
	for _, j := range resp.Jobs {
		byID[j.ID] = j
	}
	if j := byID["nudge"]; j.Relation != cronpkg.RelationRunsHere || j.Mode != "inject" || j.RunSession != "telegram:1" || j.NextRun == nil {
		t.Errorf("nudge = %+v", j)
	}
	if j := byID["brief"]; j.Relation != cronpkg.RelationReportsHere || j.Mode != "independent" || j.RunSession != "cron:brief" || j.NextRun != nil {
		t.Errorf("brief = %+v", j)
	}
	if j := byID["followup"]; j.NextRun == nil || !j.NextRun.Equal(soon) {
		t.Errorf("followup next_run = %v, want %v", j.NextRun, soon)
	}
}

func TestHandleSessionCron_BuiltinJobInItsOwnSession(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "cron:tidyup")
	ch.SetCronJobsFn(func() []cronpkg.Job {
		return []cronpkg.Job{{ID: "tidyup", Kind: cronpkg.JobKindCron, Expr: "0 4 * * 0", Task: "t", Agent: "tidyup"}}
	})
	rw := httptest.NewRecorder()
	ch.handleSessionMessages(rw, httptest.NewRequest(http.MethodGet, "/api/sessions/cron/tidyup/cron", nil))
	var resp sessionCronResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Jobs) != 1 || !resp.Jobs[0].Builtin || resp.Jobs[0].CreatedAt != nil {
		t.Fatalf("jobs = %+v, want one builtin job with no created_at", resp.Jobs)
	}
}

func TestHandleSessionCron_EmptyIsAnEmptyArray(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:x")
	ch.SetCronJobsFn(func() []cronpkg.Job { return nil })
	rw := httptest.NewRecorder()
	ch.handleSessionMessages(rw, httptest.NewRequest(http.MethodGet, "/api/sessions/web/x/cron", nil))
	if got := rw.Body.String(); rw.Code != http.StatusOK || !json.Valid([]byte(got)) || !strings.Contains(got, `"jobs":[]`) {
		t.Fatalf("status=%d body=%s", rw.Code, got)
	}
}

func TestHandleSessionCron_NoSchedulerIs503(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:x")
	rw := httptest.NewRecorder()
	ch.handleSessionMessages(rw, httptest.NewRequest(http.MethodGet, "/api/sessions/web/x/cron", nil))
	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rw.Code)
	}
}
