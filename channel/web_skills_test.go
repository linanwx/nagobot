package channel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/skills"
)

func skillLoadLines(callID, slug, ts, compressed string, heartbeatTrim bool) string {
	content := "---\nskill: " + slug + "\ntime: x\n---\n\nbody of " + slug
	tool := map[string]any{
		"role": "tool", "name": "use_skill", "tool_call_id": callID,
		"content": content, "timestamp": ts,
	}
	if compressed != "" {
		tool["compressed"] = compressed
	}
	if heartbeatTrim {
		tool["heartbeat_trim"] = true
	}
	call := map[string]any{
		"role": "assistant", "content": "", "timestamp": ts,
		"tool_calls": []map[string]any{{"id": callID, "type": "function",
			"function": map[string]any{"name": "use_skill", "arguments": `{"name":"` + slug + `"}`}}},
	}
	a, _ := json.Marshal(call)
	b, _ := json.Marshal(tool)
	return string(a) + "\n" + string(b) + "\n"
}

func newSkillsTestChannel(t *testing.T, key, jsonl string) *WebChannel {
	t.Helper()
	ch := newTestWebChannelWithSession(t, key)
	path := ch.resolveSessionFile(key, session.SessionFileName)
	if err := os.WriteFile(path, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	ch.SetSkillsFn(func() []*skills.Skill {
		return []*skills.Skill{
			{Slug: "alpha", Description: "A", Dir: "/skills/alpha",
				Prompt: "ws={{WORKSPACE}} key={{SESSIONKEY}} dir={{SESSIONDIR}} skill={{SKILLDIR}}"},
			{Slug: "beta", Description: "B", Prompt: "beta body"},
			{Slug: "gamma", Description: "C", Prompt: "gamma body"},
			{Slug: "zeta", Description: "Z", Prompt: "zeta body"},
		}
	})
	return ch
}

func getSkills(t *testing.T, ch *WebChannel, key, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/sessions/x"+query, nil)
	rw := httptest.NewRecorder()
	ch.handleSessionSkills(rw, r, key)
	return rw
}

func TestHandleSessionSkills_ListsLoadedFirstWithContextState(t *testing.T) {
	jsonl := `{"role":"user","content":"hi","timestamp":"2026-04-24T00:00:00Z"}` + "\n" +
		skillLoadLines("c1", "gamma", "2026-04-24T01:00:00Z", "", false) +
		skillLoadLines("c2", "beta", "2026-04-24T02:00:00Z", "[compressed — call use_skill to reload if needed]", false) +
		skillLoadLines("c3", "gamma", "2026-04-24T03:00:00Z", "[compressed]", false) +
		skillLoadLines("c4", "zeta", "2026-04-24T04:00:00Z", "", true) +
		skillLoadLines("c5", "gone", "2026-04-24T00:30:00Z", "", false)
	ch := newSkillsTestChannel(t, "web:sk", jsonl)

	rw := getSkills(t, ch, "web:sk", "")
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}
	var resp sessionSkillsResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	var order []string
	bySlug := map[string]sessionSkillEntry{}
	for _, e := range resp.Skills {
		order = append(order, e.Slug)
		bySlug[e.Slug] = e
	}
	// Loaded, most recent first; then the rest by slug.
	want := []string{"zeta", "gamma", "beta", "gone", "alpha"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}

	cases := []struct {
		slug                          string
		loaded, inContext, registered bool
		count                         int
	}{
		{"gamma", true, true, true, 2}, // one load still sent, one compressed
		{"beta", true, false, true, 1}, // Tier-1 compressed to a header
		{"zeta", true, false, true, 1}, // inside a trimmed heartbeat turn
		{"gone", true, true, false, 1}, // loaded, since removed from the registry
		{"alpha", false, false, true, 0},
	}
	for _, c := range cases {
		e := bySlug[c.slug]
		if e.Loaded != c.loaded || e.InContext != c.inContext || e.Registered != c.registered || e.LoadCount != c.count {
			t.Errorf("%s = %+v, want loaded=%v in_context=%v registered=%v count=%d",
				c.slug, e, c.loaded, c.inContext, c.registered, c.count)
		}
	}
	if got := bySlug["gamma"].LastLoadedAt; got == nil || got.Hour() != 3 {
		t.Errorf("gamma last_loaded_at = %v, want the 03:00 load", got)
	}
}

// A failed use_skill (unknown name, or the empty-name listing) has no skill
// header and must not mark anything loaded.
func TestHandleSessionSkills_IgnoresFailedLoads(t *testing.T) {
	call := `{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"use_skill","arguments":"{\"name\":\"beta\"}"}}],"timestamp":"2026-04-24T00:00:00Z"}` + "\n"
	result := `{"role":"tool","name":"use_skill","tool_call_id":"c1","content":"Error: skill \"beta\" not found. Available skills: alpha","timestamp":"2026-04-24T00:00:00Z"}` + "\n"
	ch := newSkillsTestChannel(t, "web:fail", call+result)

	rw := getSkills(t, ch, "web:fail", "")
	var resp sessionSkillsResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, e := range resp.Skills {
		if e.Loaded {
			t.Errorf("%s marked loaded by a failed call", e.Slug)
		}
	}
}

// The body is resolved exactly as use_skill resolves it for this session.
func TestHandleSessionSkills_BodyResolvedForTheSession(t *testing.T) {
	ch := newSkillsTestChannel(t, "web:body", `{"role":"user","content":"hi","timestamp":"2026-04-24T00:00:00Z"}`+"\n")

	rw := getSkills(t, ch, "web:body", "?name=alpha")
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}
	var resp sessionSkillBodyResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(ch.workspace, sessionsDirName, "web", "body")
	want := skills.ResolvePrompt("ws={{WORKSPACE}} key={{SESSIONKEY}} dir={{SESSIONDIR}} skill={{SKILLDIR}}", skills.PromptVars{
		Workspace: ch.workspace, SessionDir: sessionDir, SessionKey: "web:body", SkillDir: "/skills/alpha",
	})
	if resp.Prompt != want {
		t.Fatalf("prompt = %q, want %q", resp.Prompt, want)
	}
	if resp.Prompt != "ws="+ch.workspace+" key=web:body dir="+sessionDir+" skill=/skills/alpha" {
		t.Errorf("placeholders left unresolved: %q", resp.Prompt)
	}
}

func TestHandleSessionSkills_Errors(t *testing.T) {
	ch := newSkillsTestChannel(t, "web:err", `{"role":"user","content":"hi","timestamp":"2026-04-24T00:00:00Z"}`+"\n")
	if rw := getSkills(t, ch, "web:err", "?name=nope"); rw.Code != http.StatusNotFound {
		t.Errorf("unknown skill: status = %d, want 404", rw.Code)
	}
	if rw := getSkills(t, ch, "web:missing", ""); rw.Code != http.StatusNotFound {
		t.Errorf("missing session: status = %d, want 404", rw.Code)
	}
	if rw := getSkills(t, ch, "../../etc", ""); rw.Code != http.StatusBadRequest {
		t.Errorf("escaping key: status = %d, want 400", rw.Code)
	}
	ch.SetSkillsFn(nil)
	if rw := getSkills(t, ch, "web:err", ""); rw.Code != http.StatusServiceUnavailable {
		t.Errorf("no registry: status = %d, want 503", rw.Code)
	}
}
