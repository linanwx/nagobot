package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

type mockDispatchHost struct {
	currentKey      string
	callerKind      msg.CallerKind // "user" / "session" / "system" / "" (none)
	callerKey       string         // non-empty only when callerKind == "session"
	sinkLabel       string
	userFacing      bool
	agents          map[string]bool
	sessions        map[string]bool
	halted          bool
	suppressCleared bool
	sentToCaller    string
	sentToUser      string
	subagentCalls   []subagentCall
	forkCalls       []subagentCall
	wokeSessions    []wakeCall
	failAgent       string          // when non-empty, create/wake of this agent returns error
	validModels     map[string]bool // "provider:model" pairs accepted by ValidateModelOverride; nil → all accepted
}

type subagentCall struct {
	Agent, TaskID, Body string
	Provider, Model     string
}

type wakeCall struct {
	SessionKey, Body string
}

func (m *mockDispatchHost) CurrentSessionKey() string { return m.currentKey }
func (m *mockDispatchHost) CallerInfo() (msg.CallerKind, string, string) {
	return m.callerKind, m.callerKey, m.sinkLabel
}
func (m *mockDispatchHost) IsUserFacing() bool { return m.userFacing }
func (m *mockDispatchHost) AgentExists(name string) bool {
	return m.agents[name]
}
func (m *mockDispatchHost) SessionExists(key string) bool {
	return m.sessions[key]
}
func (m *mockDispatchHost) SendToCaller(_ context.Context, body string) error {
	m.sentToCaller = body
	return nil
}
func (m *mockDispatchHost) SendToUser(_ context.Context, body string) error {
	m.sentToUser = body
	return nil
}
func (m *mockDispatchHost) CreateOrWakeSubagent(_ context.Context, agent, taskID, body, overrideProvider, overrideModel string) (string, string, error) {
	if m.failAgent != "" && agent == m.failAgent {
		return "", "", fmt.Errorf("simulated failure")
	}
	m.subagentCalls = append(m.subagentCalls, subagentCall{agent, taskID, body, overrideProvider, overrideModel})
	key := m.currentKey + ":threads:" + taskID
	note := "created"
	if m.sessions[key] {
		note = "resumed"
	}
	return key, note, nil
}
func (m *mockDispatchHost) CreateOrWakeFork(_ context.Context, agent, taskID, body, overrideProvider, overrideModel string) (string, string, error) {
	if m.failAgent != "" && agent == m.failAgent {
		return "", "", fmt.Errorf("simulated failure")
	}
	m.forkCalls = append(m.forkCalls, subagentCall{agent, taskID, body, overrideProvider, overrideModel})
	key := m.currentKey + ":fork:" + taskID
	note := "forked-from:" + m.currentKey
	if m.sessions[key] {
		note = "resumed"
	}
	return key, note, nil
}
func (m *mockDispatchHost) ValidateModelOverride(provider, model string) error {
	if m.validModels == nil {
		return nil // permissive default
	}
	if m.validModels[provider+":"+model] {
		return nil
	}
	return fmt.Errorf("model override: %s/%s not available", provider, model)
}
func (m *mockDispatchHost) WakeSession(_ context.Context, sessionKey, body string) error {
	m.wokeSessions = append(m.wokeSessions, wakeCall{sessionKey, body})
	return nil
}
func (m *mockDispatchHost) SignalHalt() { m.halted = true }
func (m *mockDispatchHost) ClearSuppressSink() {
	m.suppressCleared = true
}

// runDispatch is a test helper that invokes the tool and returns the parsed
// outcome field plus the full result string for assertions.
func runDispatch(t *testing.T, host *mockDispatchHost, argsJSON string) (outcome, result string) {
	t.Helper()
	return runDispatchWithContent(t, host, argsJSON, "")
}

// runDispatchWithContent is like runDispatch but seeds the ctx with the
// assistant content field so the dispatch tool sees what the model emitted
// alongside the tool_call.
func runDispatchWithContent(t *testing.T, host *mockDispatchHost, argsJSON, content string) (outcome, result string) {
	t.Helper()
	tool := NewDispatchTool(host)
	ctx := provider.WithAssistantContent(context.Background(), content)
	result = tool.Run(ctx, json.RawMessage(argsJSON))

	// Extract outcome from result frontmatter (dispatch-specific).
	for _, line := range strings.Split(result, "\n") {
		if rest, ok := strings.CutPrefix(line, "outcome:"); ok {
			outcome = strings.TrimSpace(rest)
			break
		}
	}
	return outcome, result
}

func TestDispatch_EmptySendsIsSilentTerminate(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	outcome, _ := runDispatch(t, host, `{"sends": []}`)
	if outcome != "turn-terminated-silent" {
		t.Fatalf("expected silent terminate, got %q", outcome)
	}
	if !host.halted {
		t.Fatal("expected SignalHalt to be called on empty sends")
	}
}

func TestDispatch_OmittedSendsIsSilentTerminate(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	outcome, res := runDispatch(t, host, `{}`)
	if outcome != "turn-terminated-silent" {
		t.Fatalf("expected silent, got %q; %s", outcome, res)
	}
	if !host.halted {
		t.Fatal("expected halt")
	}
}

// caller:user succeeds when actual caller kind is "user".
func TestDispatch_CallerUser_OK(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:123",
		callerKind: "user",
		userFacing: true,
	}
	outcome, _ := runDispatch(t, host, `{"sends": [{"to": "caller:user", "body": "hi"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q", outcome)
	}
	if host.sentToCaller != "hi" {
		t.Errorf("expected caller body=hi, got %q", host.sentToCaller)
	}
	if !host.halted {
		t.Fatal("expected halt")
	}
}

// caller:session succeeds when actual caller kind is "session".
func TestDispatch_CallerSession_OK(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:123",
		callerKind: "session",
		callerKey:  "cli",
	}
	outcome, _ := runDispatch(t, host, `{"sends": [{"to": "caller:session", "body": "hi"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q", outcome)
	}
	if host.sentToCaller != "hi" {
		t.Errorf("expected caller body=hi, got %q", host.sentToCaller)
	}
}

// dispatch(to=caller:user) when caller is the channel user must NOT emit
// any "redundant" or "noUserReminder" hint — the user did receive the
// message, and forcing a single canonical delivery path is not nagobot's
// policy (naive text and dispatch are equivalent).
func TestDispatch_CallerUser_NoHintsWhenCallerIsUser(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "user",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "caller:user", "body": "hi"}]}`)
	if strings.Contains(res, "redundant") {
		t.Errorf("redundant-delivery hint should be removed; got: %s", res)
	}
	if strings.Contains(res, "had no to=user entry") {
		t.Errorf("noUserReminder must NOT fire when reach-user path is taken; got: %s", res)
	}
}

// Same for to=user — no spurious hint.
func TestDispatch_User_NoHintsWhenCallerIsUser(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "user",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "user", "body": "hi"}]}`)
	if strings.Contains(res, "redundant") {
		t.Errorf("redundant-delivery hint should be removed; got: %s", res)
	}
}

// Hint MUST NOT fire when caller is another session — sub-session replying back
// to a user channel via to=user is legitimate, not redundant.
func TestDispatch_User_NoHintWhenCallerIsSession(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "session",
		callerKey:  "telegram:1",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "user", "body": "hi"}]}`)
	if strings.Contains(res, "redundant") {
		t.Errorf("hint should NOT fire for session caller; got: %s", res)
	}
}

// Hint MUST NOT fire when caller is system — system wakes don't auto-deliver.
func TestDispatch_User_NoHintWhenCallerIsSystem(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "system",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "user", "body": "hi"}]}`)
	if strings.Contains(res, "redundant") {
		t.Errorf("hint should NOT fire for system caller; got: %s", res)
	}
}

// caller:user rejected when actual caller is another session.
func TestDispatch_CallerUser_MismatchSession(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:1",
		callerKind: "session",
		callerKey:  "cli",
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "caller:user", "body": "hi"}]}`)
	if !strings.Contains(res, "validation-error") {
		t.Errorf("expected validation-error, got: %s", res)
	}
	if !strings.Contains(res, "caller:session") {
		t.Errorf("error should suggest caller:session; got: %s", res)
	}
	if host.halted {
		t.Error("expected not-halted on validation error")
	}
}

// caller:session rejected when actual caller is the channel user.
func TestDispatch_CallerSession_MismatchUser(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:1",
		callerKind: "user",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "caller:session", "body": "hi"}]}`)
	if !strings.Contains(res, "validation-error") {
		t.Errorf("expected validation-error, got: %s", res)
	}
	if !strings.Contains(res, "caller:user") {
		t.Errorf("error should suggest caller:user; got: %s", res)
	}
}

// caller:user rejected on system caller (cron/heartbeat/compression drop sink).
func TestDispatch_CallerUser_RejectsSystemCaller(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:1",
		callerKind: "system",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "caller:user", "body": "hi"}]}`)
	if !strings.Contains(res, "validation-error") {
		t.Errorf("expected validation-error, got: %s", res)
	}
	if !strings.Contains(res, "system") {
		t.Errorf("error should mention system caller; got: %s", res)
	}
}

// caller:session rejected on system caller.
func TestDispatch_CallerSession_RejectsSystemCaller(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cron:job",
		callerKind: "system",
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "caller:session", "body": "hi"}]}`)
	if !strings.Contains(res, "validation-error") {
		t.Errorf("expected validation-error, got: %s", res)
	}
}

// Bare "caller" is no longer a valid target — must be caller:user or caller:session.
func TestDispatch_BareCallerRejected(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:1",
		callerKind: "user",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "caller", "body": "hi"}]}`)
	if !strings.Contains(res, "unknown to") {
		t.Errorf("expected unknown-to error for bare caller, got: %s", res)
	}
	if host.halted {
		t.Error("validation error must not halt")
	}
}

func TestDispatch_User(t *testing.T) {
	host := &mockDispatchHost{currentKey: "telegram:42", userFacing: true, callerKind: "user"}
	outcome, _ := runDispatch(t, host, `{"sends": [{"to": "user", "body": "ping"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q", outcome)
	}
	if host.sentToUser != "ping" {
		t.Errorf("user delivery: %q", host.sentToUser)
	}
}

func TestDispatch_UserRejectedForNonUserFacing(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli:threads:bg", userFacing: false, callerKind: "session"}
	_, res := runDispatch(t, host, `{"sends": [{"to": "user", "body": "ping"}]}`)
	if !strings.Contains(res, "not user-facing") {
		t.Errorf("expected not-user-facing error, got: %s", res)
	}
}

// caller:session + to=user coexist: caller is another session, user is channel user.
func TestDispatch_CallerSessionAndUserCoexist(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:42",
		callerKind: "session",
		callerKey:  "cli",
		userFacing: true,
	}
	outcome, _ := runDispatch(t, host, `{"sends": [
		{"to": "caller:session", "body": "back to waker"},
		{"to": "user", "body": "to channel user"}
	]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q", outcome)
	}
	if host.sentToCaller != "back to waker" {
		t.Errorf("caller: %q", host.sentToCaller)
	}
	if host.sentToUser != "to channel user" {
		t.Errorf("user: %q", host.sentToUser)
	}
}

// caller:user reaches the channel user, so the "no to=user" reminder must NOT fire.
func TestDispatch_NoReminderWhenCallerUser(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:42",
		callerKind: "user",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "caller:user", "body": "hi"}]}`)
	if strings.Contains(res, "no to=user entry") {
		t.Errorf("noUserReminder must not fire when caller is the channel user; got:\n%s", res)
	}
}

// caller:session goes to another session, NOT the user. The reminder must fire.
// On a user-facing session woken by a peer (callerKind=session, e.g. parent
// receiving child_completed), dispatch with no user-facing target is now a
// hard validation error — the user must see progress. Previously this case
// only emitted a soft "noUserReminder" appended to a successful result.
func TestDispatch_UserFacingSessionCallerRejectedWithoutUserTarget(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:42",
		callerKind: "session",
		callerKey:  "cli",
		userFacing: true,
	}
	outcome, res := runDispatch(t, host, `{"sends": [{"to": "caller:session", "body": "hi"}]}`)
	if outcome != "validation-error" {
		t.Fatalf("outcome=%q, want validation-error; full result:\n%s", outcome, res)
	}
	if !strings.Contains(res, "user-facing target") {
		t.Errorf("expected user-progress rejection text; got:\n%s", res)
	}
	if host.halted {
		t.Error("validation error must not halt the turn")
	}
	if host.sentToCaller != "" {
		t.Errorf("rejected dispatch must not deliver; got sentToCaller=%q", host.sentToCaller)
	}
}

// Adding a to=user (or to=caller:user) send to the same scenario satisfies
// the user-progress rule and the dispatch executes normally.
func TestDispatch_UserFacingSessionCallerAcceptedWithUserTarget(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:42",
		callerKind: "session",
		callerKey:  "cli",
		userFacing: true,
	}
	outcome, _ := runDispatch(t, host, `{"sends": [
		{"to": "user", "body": "child reported back: ..."},
		{"to": "caller:session", "body": "ack"}
	]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q, want turn-terminated", outcome)
	}
	if host.sentToUser == "" {
		t.Error("expected to=user delivery to fire")
	}
	if host.sentToCaller == "" {
		t.Error("expected to=caller:session delivery to fire")
	}
}

// dispatch({}) is the silent-termination escape valve — ALWAYS allowed,
// even on a user-facing session woken interactively. Highest priority.
func TestDispatch_EmptySendsAllowedOnUserFacingSession(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:42",
		callerKind: "user",
		userFacing: true,
	}
	outcome, _ := runDispatch(t, host, `{"sends": []}`)
	if outcome != "turn-terminated-silent" {
		t.Fatalf("outcome=%q, want turn-terminated-silent (dispatch({}) is unconditional)", outcome)
	}
	if !host.halted {
		t.Error("dispatch({}) must SignalHalt")
	}
}

// Explicit to=user always suppresses the reminder regardless of caller kind.
func TestDispatch_NoReminderWhenToUserExplicit(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:42",
		callerKind: "session",
		callerKey:  "cli",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "user", "body": "hi"}]}`)
	if strings.Contains(res, "no to=user entry") {
		t.Errorf("noUserReminder must not fire when to=user is present; got:\n%s", res)
	}
}

func TestDispatch_Subagent(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "user",
		agents:     map[string]bool{"search": true},
	}
	outcome, res := runDispatch(t, host,
		`{"sends": [{"to": "subagent", "params": {"agent": "search", "task_id": "bg-check"}, "body": "查 X"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q, result=%s", outcome, res)
	}
	if len(host.subagentCalls) != 1 {
		t.Fatalf("expected 1 subagent call, got %d", len(host.subagentCalls))
	}
	if host.subagentCalls[0].TaskID != "bg-check" {
		t.Errorf("bad task_id: %+v", host.subagentCalls[0])
	}
	if !strings.Contains(res, "cli:threads:bg-check") {
		t.Errorf("expected resolved key in result, got: %s", res)
	}
}

func TestDispatch_SubagentMissingAgent(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user", agents: map[string]bool{}}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "subagent", "params": {"agent": "nonexistent", "task_id": "x"}, "body": "go"}]}`)
	if !strings.Contains(res, "validation-error") {
		t.Errorf("expected validation-error, got: %s", res)
	}
	if len(host.subagentCalls) != 0 {
		t.Error("expected no execution on validation error")
	}
}

func TestDispatch_SubagentAgentOptional(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	outcome, res := runDispatch(t, host,
		`{"sends": [{"to": "subagent", "params": {"task_id": "bg-check"}, "body": "go"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("expected success with empty agent (session default), got %q; %s", outcome, res)
	}
	if len(host.subagentCalls) != 1 {
		t.Fatalf("expected 1 subagent call, got %d", len(host.subagentCalls))
	}
	if host.subagentCalls[0].Agent != "" {
		t.Errorf("expected empty agent passthrough, got %q", host.subagentCalls[0].Agent)
	}
}

func TestDispatch_SubagentBadTaskID(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user", agents: map[string]bool{"s": true}}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "subagent", "params": {"agent": "s", "task_id": "BAD ID!"}, "body": "x"}]}`)
	if !strings.Contains(res, "validation-error") {
		t.Errorf("expected validation-error for bad task_id, got: %s", res)
	}
}

func TestDispatch_Fork(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:1",
		callerKind: "user",
		agents:     map[string]bool{"analyst": true},
	}
	outcome, res := runDispatch(t, host,
		`{"sends": [{"to": "fork", "params": {"agent": "analyst", "task_id": "hypo-a"}, "body": "explore"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q; %s", outcome, res)
	}
	if len(host.forkCalls) != 1 {
		t.Fatalf("expected 1 fork call, got %d", len(host.forkCalls))
	}
	if !strings.Contains(res, "telegram:1:fork:hypo-a") {
		t.Errorf("expected fork key in result, got: %s", res)
	}
}

// A subagent dispatch carrying a valid provider/model override passes
// validation and the override is plumbed through to the host call.
func TestDispatch_SubagentModelOverride_OK(t *testing.T) {
	host := &mockDispatchHost{
		currentKey:  "cli",
		callerKind:  "user",
		agents:      map[string]bool{"s": true},
		validModels: map[string]bool{"openrouter:anthropic/claude-opus-4.6": true},
	}
	outcome, res := runDispatch(t, host,
		`{"sends": [{"to": "subagent", "params": {"agent": "s", "task_id": "hard-q", "provider": "openrouter", "model": "anthropic/claude-opus-4.6"}, "body": "go"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q; %s", outcome, res)
	}
	if len(host.subagentCalls) != 1 {
		t.Fatalf("expected 1 subagent call, got %d", len(host.subagentCalls))
	}
	if got := host.subagentCalls[0]; got.Provider != "openrouter" || got.Model != "anthropic/claude-opus-4.6" {
		t.Errorf("override not plumbed: provider=%q model=%q", got.Provider, got.Model)
	}
}

// An unavailable provider/model override fails validation (loud, no call made).
func TestDispatch_SubagentModelOverride_Unavailable(t *testing.T) {
	host := &mockDispatchHost{
		currentKey:  "cli",
		callerKind:  "user",
		agents:      map[string]bool{"s": true},
		validModels: map[string]bool{}, // nothing valid
	}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "subagent", "params": {"agent": "s", "task_id": "hard-q", "provider": "openrouter", "model": "nope/model"}, "body": "go"}]}`)
	if !strings.Contains(res, "validation-error") || !strings.Contains(res, "not available") {
		t.Errorf("expected unavailable-override validation error, got: %s", res)
	}
	if len(host.subagentCalls) != 0 {
		t.Errorf("no host call should be made on validation failure, got %d", len(host.subagentCalls))
	}
}

// provider without model (and vice versa) is rejected — they must be paired.
func TestDispatch_SubagentModelOverride_RequiresBoth(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user", agents: map[string]bool{"s": true}}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "subagent", "params": {"agent": "s", "task_id": "q", "provider": "openrouter"}, "body": "go"}]}`)
	if !strings.Contains(res, "validation-error") || !strings.Contains(res, "set together") {
		t.Errorf("expected paired-fields validation error, got: %s", res)
	}
}

// Model override on a non-subagent/fork target is rejected.
func TestDispatch_ModelOverride_WrongTarget(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user", userFacing: true}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "user", "params": {"provider": "openrouter", "model": "anthropic/claude-opus-4.6"}, "body": "hi"}]}`)
	if !strings.Contains(res, "validation-error") || !strings.Contains(res, "override applies to to=subagent/fork only") {
		t.Errorf("expected wrong-target validation error, got: %s", res)
	}
}

func TestDispatch_ForkNested(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:1:fork:a",
		callerKind: "session",
		callerKey:  "telegram:1",
		agents:     map[string]bool{"analyst": true},
	}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "fork", "params": {"agent": "analyst", "task_id": "b"}, "body": "deeper"}]}`)
	if !strings.Contains(res, "telegram:1:fork:a:fork:b") {
		t.Errorf("expected nested fork key, got: %s", res)
	}
}

func TestDispatch_WakeSession(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:1",
		callerKind: "user",
		sessions:   map[string]bool{"telegram:2": true},
	}
	outcome, _ := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"session_key": "telegram:2"}, "body": "ping"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q", outcome)
	}
	if len(host.wokeSessions) != 1 || host.wokeSessions[0].SessionKey != "telegram:2" {
		t.Errorf("expected telegram:2 wake, got %+v", host.wokeSessions)
	}
}

func TestDispatch_WakeSessionMissing(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"session_key": "telegram:999"}, "body": "ping"}]}`)
	if !strings.Contains(res, "validation-error") {
		t.Errorf("expected validation-error, got: %s", res)
	}
}

func TestDispatch_SelfReferenceRejected(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:1",
		callerKind: "user",
		sessions:   map[string]bool{"telegram:1": true},
	}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"session_key": "telegram:1"}, "body": "me"}]}`)
	if !strings.Contains(res, "self-reference") {
		t.Errorf("expected self-reference error, got: %s", res)
	}
}

func TestDispatch_MultipleTargets(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "user",
		userFacing: true,
		agents:     map[string]bool{"search": true, "analyst": true},
		sessions:   map[string]bool{"telegram:2": true},
	}
	outcome, res := runDispatch(t, host,
		`{"sends": [
			{"to": "caller:user", "body": "working on it"},
			{"to": "subagent", "params": {"agent": "search", "task_id": "bg"}, "body": "查"},
			{"to": "fork", "params": {"agent": "analyst", "task_id": "hypo"}, "body": "branch"},
			{"to": "session", "params": {"session_key": "telegram:2"}, "body": "sync"}
		]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q; %s", outcome, res)
	}
	if host.sentToCaller != "working on it" {
		t.Errorf("caller body: %q", host.sentToCaller)
	}
	if len(host.subagentCalls) != 1 || len(host.forkCalls) != 1 || len(host.wokeSessions) != 1 {
		t.Errorf("unexpected call counts: sub=%d fork=%d wake=%d",
			len(host.subagentCalls), len(host.forkCalls), len(host.wokeSessions))
	}
	if !host.halted {
		t.Error("expected halt after success")
	}
}

// Two caller replies of the same kind in one batch collapse to duplicate.
func TestDispatch_DuplicateCallerInBatchRejected(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:1",
		callerKind: "user",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [
		{"to": "caller:user", "body": "a"},
		{"to": "caller:user", "body": "b"}
	]}`)
	if !strings.Contains(res, "duplicate target in batch") {
		t.Errorf("expected duplicate-target error, got: %s", res)
	}
}

func TestDispatch_DuplicateInBatchRejected(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "user",
		agents:     map[string]bool{"s": true},
	}
	_, res := runDispatch(t, host,
		`{"sends": [
			{"to": "subagent", "params": {"agent": "s", "task_id": "x"}, "body": "1"},
			{"to": "subagent", "params": {"agent": "s", "task_id": "x"}, "body": "2"}
		]}`)
	if !strings.Contains(res, "duplicate target in batch") {
		t.Errorf("expected duplicate error, got: %s", res)
	}
}

func TestDispatch_UnknownToRejected(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "blackhole", "body": "void"}]}`)
	if !strings.Contains(res, "unknown to") {
		t.Errorf("expected unknown-to error, got: %s", res)
	}
}

func TestDispatch_BodyRequired(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "caller:user", "body": "  "}]}`)
	if !strings.Contains(res, "body is required") {
		t.Errorf("expected body-required error, got: %s", res)
	}
}

func TestDispatch_ValidationErrorDoesNotHalt(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	runDispatch(t, host, `{"sends": [{"to": "caller:user", "body": ""}]}`)
	if host.halted {
		t.Error("validation errors must not halt the turn — model needs to retry")
	}
}

func TestDispatch_ResultIncludesInlineBody(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:1",
		callerKind: "session",
		callerKey:  "cron:briefing",
		sinkLabel:  "your reply will be forwarded to caller session cron:briefing",
	}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "caller:session", "body": "hello world this is the reply"}]}`)
	if !strings.Contains(res, `Replied "hello world this is the reply" to the caller session cron:briefing`) {
		t.Errorf("expected inline quoted body in caller description, got:\n%s", res)
	}
}

func TestDispatch_BodyPreviewTruncatesAt100Runes(t *testing.T) {
	host := &mockDispatchHost{currentKey: "telegram:1", userFacing: true, callerKind: "user"}
	long := strings.Repeat("a", 150)
	_, res := runDispatch(t, host,
		fmt.Sprintf(`{"sends": [{"to": "user", "body": %q}]}`, long))
	expected := `Sent "` + strings.Repeat("a", 100) + `..." to your channel user`
	if !strings.Contains(res, expected) {
		t.Errorf("expected truncated body inline, got:\n%s", res)
	}
	if strings.Contains(res, strings.Repeat("a", 150)) {
		t.Error("expected body to be truncated, but full body appeared in result")
	}
}

func TestDispatch_BodyPreviewCollapsesNewlines(t *testing.T) {
	host := &mockDispatchHost{currentKey: "telegram:1", userFacing: true, callerKind: "user"}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "user", "body": "line one\nline two\r\nline three"}]}`)
	if !strings.Contains(res, `"line one line two line three"`) {
		t.Errorf("expected newlines collapsed to spaces in inline body, got:\n%s", res)
	}
}

func TestDispatch_ExecFailureHaltsButReportsErrors(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "user",
		agents:     map[string]bool{"search": true, "broken": true},
		failAgent:  "broken",
	}
	_, res := runDispatch(t, host,
		`{"sends": [
			{"to": "subagent", "params": {"agent": "search", "task_id": "ok"}, "body": "a"},
			{"to": "subagent", "params": {"agent": "broken", "task_id": "bad"}, "body": "b"}
		]}`)
	if !strings.Contains(res, "partial-failure") {
		t.Errorf("expected partial-failure, got: %s", res)
	}
	if !host.halted {
		t.Error("expected halt after execution attempted (successes unrecoverable)")
	}
}

// dispatch + non-empty assistant content is a validation error: no sends
// execute, turn does NOT halt, and the offending content is echoed in the
// error so the model can see what to remove or move into a body.
func TestDispatch_RejectsAssistantContent(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:123",
		callerKind: "user",
		userFacing: true,
	}
	outcome, res := runDispatchWithContent(t, host,
		`{"sends": [{"to": "caller:user", "body": "hi"}]}`,
		"I will go check that for you now.")
	if outcome != "validation-error" {
		t.Fatalf("expected validation-error, got %q; %s", outcome, res)
	}
	if host.halted {
		t.Error("turn should NOT halt on validation error — model should re-call")
	}
	if host.sentToCaller != "" {
		t.Errorf("no send should have executed, but sentToCaller=%q", host.sentToCaller)
	}
	if !strings.Contains(res, "I will go check that for you now.") {
		t.Errorf("expected offending content echoed in error, got: %s", res)
	}
	// The fix is singular: always move text into a send body. The old
	// "just don't call dispatch / end with the assistant message" escape hatch
	// must NOT be offered.
	if !strings.Contains(res, "move ALL user-facing text into the appropriate send body") {
		t.Errorf("error should mandate moving text into send body, got: %s", res)
	}
	for _, gone := range []string{"do NOT call dispatch at all", "Fix by ONE of", "end the turn with the assistant message"} {
		if strings.Contains(res, gone) {
			t.Errorf("error should no longer offer %q, got: %s", gone, res)
		}
	}
}

// Whitespace-only assistant content is treated as empty — dispatch proceeds.
func TestDispatch_AllowsWhitespaceAssistantContent(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:123",
		callerKind: "user",
		userFacing: true,
	}
	outcome, res := runDispatchWithContent(t, host,
		`{"sends": [{"to": "caller:user", "body": "hi"}]}`,
		"  \n\t\n  ")
	if outcome != "turn-terminated" {
		t.Fatalf("expected turn-terminated, got %q; %s", outcome, res)
	}
	if host.sentToCaller != "hi" {
		t.Errorf("expected send to execute, got sentToCaller=%q", host.sentToCaller)
	}
}

// dispatch({}) with non-empty content is also rejected — no exception for
// silent termination, since the content still has no recipient.
func TestDispatch_RejectsAssistantContent_EmptySends(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user", userFacing: true}
	outcome, _ := runDispatchWithContent(t, host, `{}`, "thinking out loud")
	if outcome != "validation-error" {
		t.Fatalf("expected validation-error for content+empty-sends, got %q", outcome)
	}
	if host.halted {
		t.Error("turn should not halt on validation error")
	}
}

// Long content is truncated in the error preview to keep the validation
// message bounded.
func TestDispatch_RejectsAssistantContent_TruncatesPreview(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user", userFacing: true}
	long := strings.Repeat("a", 500)
	_, res := runDispatchWithContent(t, host,
		`{"sends": [{"to": "caller:user", "body": "ok"}]}`,
		long)
	if !strings.Contains(res, "...") {
		t.Error("expected long content to be truncated with ...")
	}
}

func TestDispatch_WakeSessionEndpoint_Existing(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cron:weekly-thanks",
		callerKind: "system",
		sessions:   map[string]bool{"wecom:LiNan": true},
	}
	outcome, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"channel": "wecom", "user_id": "LiNan"}, "body": "summarize uploads"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q result=%s", outcome, res)
	}
	if len(host.wokeSessions) != 1 || host.wokeSessions[0].SessionKey != "wecom:LiNan" {
		t.Errorf("expected wecom:LiNan wake, got %+v", host.wokeSessions)
	}
	if strings.Contains(res, "Created new session") {
		t.Errorf("existing session must not be reported as created: %s", res)
	}
}

func TestDispatch_WakeSessionEndpoint_CreatesMissing(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cron:weekly-thanks", callerKind: "system"}
	outcome, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"channel": "wecom", "user_id": "ZhaoJing"}, "body": "thank for uploads"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q result=%s", outcome, res)
	}
	if len(host.wokeSessions) != 1 || host.wokeSessions[0].SessionKey != "wecom:ZhaoJing" {
		t.Errorf("expected wecom:ZhaoJing wake, got %+v", host.wokeSessions)
	}
	if !strings.Contains(res, "Created new session wecom:ZhaoJing") {
		t.Errorf("expected created note, got: %s", res)
	}
}

func TestDispatch_WakeSessionEndpoint_BothFormsRejected(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "user",
		sessions:   map[string]bool{"wecom:LiNan": true},
	}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"session_key": "wecom:LiNan", "channel": "wecom", "user_id": "LiNan"}, "body": "x"}]}`)
	if !strings.Contains(res, "validation-error") || !strings.Contains(res, "not both") {
		t.Errorf("expected both-forms rejection, got: %s", res)
	}
}

func TestDispatch_WakeSessionEndpoint_ChannelWithoutUserID(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"channel": "wecom"}, "body": "x"}]}`)
	if !strings.Contains(res, "validation-error") || !strings.Contains(res, "channel and user_id must both be set") {
		t.Errorf("expected paired-fields error, got: %s", res)
	}
}

func TestDispatch_WakeSession_NeitherFormRejected(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "body": "x"}]}`)
	if !strings.Contains(res, "validation-error") || !strings.Contains(res, "session requires params") {
		t.Errorf("expected missing-form error, got: %s", res)
	}
}

func TestDispatch_WakeSessionEndpoint_UnknownChannel(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"channel": "wechat", "user_id": "LiNan"}, "body": "x"}]}`)
	if !strings.Contains(res, "validation-error") || !strings.Contains(res, `unknown channel "wechat"`) {
		t.Errorf("expected unknown-channel error, got: %s", res)
	}
}

func TestDispatch_WakeSessionEndpoint_SelfReferenceRejected(t *testing.T) {
	host := &mockDispatchHost{currentKey: "wecom:LiNan", callerKind: "user"}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"channel": "wecom", "user_id": "LiNan"}, "body": "me"}]}`)
	if !strings.Contains(res, "self-reference") {
		t.Errorf("expected self-reference error, got: %s", res)
	}
}

func TestDispatch_WakeSessionEndpoint_SubsessionInfixRejected(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"channel": "telegram", "user_id": "123:threads:bg"}, "body": "x"}]}`)
	if !strings.Contains(res, "validation-error") || !strings.Contains(res, "cannot address subagent/fork sessions") {
		t.Errorf("expected infix rejection, got: %s", res)
	}
}

func TestDispatch_WakeSessionEndpoint_DedupAgainstKeyForm(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "user",
		sessions:   map[string]bool{"wecom:LiNan": true},
	}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"session_key": "wecom:LiNan"}, "body": "a"}, {"to": "session", "params": {"channel": "wecom", "user_id": "LiNan"}, "body": "b"}]}`)
	if !strings.Contains(res, "validation-error") || !strings.Contains(res, "duplicate target in batch: wecom:LiNan") {
		t.Errorf("expected cross-form dedup error, got: %s", res)
	}
}

func TestDispatch_WakeSessionEndpoint_GroupConvention(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cron:weekly-thanks", callerKind: "system"}
	outcome, _ := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"channel": "wecom", "user_id": "group:wrNbLgXQAA"}, "body": "weekly digest"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q", outcome)
	}
	if len(host.wokeSessions) != 1 || host.wokeSessions[0].SessionKey != "wecom:group:wrNbLgXQAA" {
		t.Errorf("expected wecom:group:wrNbLgXQAA wake, got %+v", host.wokeSessions)
	}
}

// --- Field admission: every misplaced field is rejected, none is ignored -----
//
// channel/user_id used to be accepted-then-ignored on the four non-session
// targets, while every other misplaced field was rejected. Silence reads as
// acceptance: the model got its subagent and no channel delivery, with no error
// to learn from.

func TestDispatch_ChannelRejectedOnSubagent(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "system", agents: map[string]bool{"worker": true}}
	outcome, result := runDispatch(t, host,
		`{"sends": [{"to": "subagent", "params": {"task_id": "t1", "agent": "worker", "channel": "telegram", "user_id": "123"}, "body": "go"}]}`)
	if outcome != "validation-error" {
		t.Fatalf("outcome=%q, result=%s", outcome, result)
	}
	if !strings.Contains(result, "channel") || !strings.Contains(result, "user_id") {
		t.Errorf("expected both offending fields named, got: %s", result)
	}
	if len(host.subagentCalls) != 0 {
		t.Errorf("no send may execute when validation fails, got %+v", host.subagentCalls)
	}
}

func TestDispatch_ChannelRejectedOnCallerUser(t *testing.T) {
	host := &mockDispatchHost{currentKey: "telegram:1", callerKind: "user", userFacing: true}
	outcome, result := runDispatch(t, host,
		`{"sends": [{"to": "caller:user", "params": {"channel": "telegram", "user_id": "999"}, "body": "hi"}]}`)
	if outcome != "validation-error" {
		t.Fatalf("outcome=%q, result=%s", outcome, result)
	}
	if !strings.Contains(result, "does not accept") {
		t.Errorf("expected a does-not-accept rejection, got: %s", result)
	}
	if host.sentToCaller != "" {
		t.Errorf("nothing may be delivered on a validation error, sent: %q", host.sentToCaller)
	}
}

// An unknown key inside a send is rejected by parseArgs' recursive guard. The
// motivating case is `delay`: dispatch has no delay parameter, and dropping it
// silently woke the target immediately while the model believed it had
// scheduled a future wake.
func TestDispatch_UnknownFieldInsideSendRejected(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user", userFacing: true}
	_, result := runDispatch(t, host,
		`{"sends": [{"to": "caller:user", "body": "later", "delay": "1h"}]}`)
	if !strings.Contains(result, "sends[0].delay") {
		t.Fatalf("expected sends[0].delay to be rejected with its path, got: %s", result)
	}
	if host.sentToCaller != "" {
		t.Errorf("nothing may be delivered, sent: %q", host.sentToCaller)
	}
}

// --- Whitespace normalization: one value, one identity ----------------------

func TestDispatch_SessionKeyTrimmedBeforeLookup(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "system",
		sessions:   map[string]bool{"telegram:42": true},
	}
	outcome, result := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"session_key": "telegram:42 "}, "body": "ping"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("a trailing space must not fail an existing session: outcome=%q, result=%s", outcome, result)
	}
	if len(host.wokeSessions) != 1 || host.wokeSessions[0].SessionKey != "telegram:42" {
		t.Errorf("expected the trimmed key to be woken, got %+v", host.wokeSessions)
	}
}

// The self-reference guard used to compare untrimmed while the hasKey gate
// trimmed, so a trailing space walked past it and was caught only by the
// existence check further down.
func TestDispatch_SelfReferenceNotBypassableByWhitespace(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli:main",
		callerKind: "system",
		sessions:   map[string]bool{"cli:main": true},
	}
	outcome, result := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"session_key": "cli:main "}, "body": "self"}]}`)
	if outcome != "validation-error" {
		t.Fatalf("outcome=%q, result=%s", outcome, result)
	}
	if !strings.Contains(result, "self-reference") {
		t.Errorf("expected the self-reference guard to fire, got: %s", result)
	}
	if len(host.wokeSessions) != 0 {
		t.Errorf("self-wake must not execute, got %+v", host.wokeSessions)
	}
}

// A model that cannot omit fields sends them as whitespace. That must read as
// "not provided" everywhere, not as "provided" on the reject path.
func TestDispatch_WhitespaceAgentTreatedAsAbsent(t *testing.T) {
	host := &mockDispatchHost{currentKey: "telegram:1", callerKind: "user", userFacing: true}
	outcome, result := runDispatch(t, host,
		`{"sends": [{"to": "caller:user", "params": {"agent": " ", "task_id": ""}, "body": "hi"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("whitespace agent must read as absent: outcome=%q, result=%s", outcome, result)
	}
	if host.sentToCaller != "hi" {
		t.Errorf("expected delivery to caller, sent: %q", host.sentToCaller)
	}
}

func TestDispatch_WhitespaceAgentOnSubagentUsesSessionDefault(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "system"}
	outcome, result := runDispatch(t, host,
		`{"sends": [{"to": "subagent", "params": {"task_id": "t1", "agent": "  "}, "body": "go"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q, result=%s", outcome, result)
	}
	if len(host.subagentCalls) != 1 || host.subagentCalls[0].Agent != "" {
		t.Errorf("expected the session default (empty agent), got %+v", host.subagentCalls)
	}
}

// --- params dictionary behaviors --------------------------------------------

// The exact failure shape observed live (2026-07-13): a model emitted the old
// flat layout with channel:"discord" on to=user. Flat addressing keys no
// longer exist — parseArgs rejects them by path so the model sees exactly
// which key is wrong and what the send object accepts.
func TestDispatch_FlatAddressingKeysRejectedByPath(t *testing.T) {
	host := &mockDispatchHost{currentKey: "discord:1474429571540582463", callerKind: "system", userFacing: true}
	_, res := runDispatch(t, host,
		`{"sends":[{"agent":"","body":"💊 今晚已吃药 ✅","channel":"discord","model":"","provider":"","session_key":"","task_id":"","to":"user","user_id":""}]}`)
	if !strings.Contains(res, "sends[0].channel") || !strings.Contains(res, "params") {
		t.Errorf("expected path-named rejection pointing at params, got: %s", res)
	}
	if host.sentToUser != "" {
		t.Errorf("nothing may be delivered, sent: %q", host.sentToUser)
	}
}

// A strict-structured-output model that cannot omit keys blanks them instead.
// An all-empty params dictionary must read as "no params", not as misplaced
// addressing — this send must deliver.
func TestDispatch_AllEmptyParamsTreatedAsAbsent(t *testing.T) {
	host := &mockDispatchHost{currentKey: "discord:123", callerKind: "system", userFacing: true}
	outcome, res := runDispatch(t, host,
		`{"sends": [{"to": "user", "params": {"agent": "", "task_id": "", "provider": "", "model": "", "session_key": "", "channel": "", "user_id": ""}, "body": "💊 今晚已吃药 ✅"}]}`)
	if outcome != "turn-terminated" {
		t.Fatalf("all-empty params must be ignored: outcome=%q, result=%s", outcome, res)
	}
	if host.sentToUser != "💊 今晚已吃药 ✅" {
		t.Errorf("expected delivery, sent: %q", host.sentToUser)
	}
}

// A misplaced non-empty params key on a to/body-only target is rejected with
// copy-paste guidance: the corrected JSON to resend.
func TestDispatch_MisplacedParamsGuidanceIncludesCorrectedJSON(t *testing.T) {
	host := &mockDispatchHost{currentKey: "discord:123", callerKind: "system", userFacing: true}
	outcome, res := runDispatch(t, host,
		`{"sends": [{"to": "user", "params": {"channel": "discord"}, "body": "hi"}]}`)
	if outcome != "validation-error" {
		t.Fatalf("outcome=%q, result=%s", outcome, res)
	}
	if !strings.Contains(res, `{"sends":[{"body":"hi","to":"user"}]}`) {
		t.Errorf("expected corrected JSON in guidance, got: %s", res)
	}
}

// A params key no target understands is rejected by name with the valid key list.
func TestDispatch_UnknownParamsKeyRejected(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user", userFacing: true}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "caller:user", "params": {"delay": "1h"}, "body": "later"}]}`)
	if !strings.Contains(res, "unknown params key(s): delay") || !strings.Contains(res, "task_id") {
		t.Errorf("expected unknown-key rejection with valid key list, got: %s", res)
	}
	if host.sentToCaller != "" {
		t.Errorf("nothing may be delivered, sent: %q", host.sentToCaller)
	}
}

// The to=session self-reference rejection points back to to=user with the
// exact JSON — previously it dead-ended (the to=user error suggested
// to=session, the to=session error rejected self-reference, and the model
// ping-ponged between them).
func TestDispatch_SelfReferenceGuidesToUser(t *testing.T) {
	host := &mockDispatchHost{currentKey: "discord:123", callerKind: "system", userFacing: true}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "session", "params": {"channel": "discord", "user_id": "123"}, "body": "ping"}]}`)
	if !strings.Contains(res, "self-reference") || !strings.Contains(res, `{"sends":[{"body":"ping","to":"user"}]}`) {
		t.Errorf("expected self-reference error with to=user corrected JSON, got: %s", res)
	}
}

// --- Solo rule: dispatch only terminates when it is the sole tool call ---

// runDispatchBatched invokes the tool with a ctx that declares the assistant
// message carried batchSize tool calls in total.
func runDispatchBatched(t *testing.T, host *mockDispatchHost, argsJSON string, batchSize int) (outcome, result string) {
	t.Helper()
	tool := NewDispatchTool(host)
	ctx := provider.WithToolBatchSize(context.Background(), batchSize)
	result = tool.Run(ctx, json.RawMessage(argsJSON))
	for _, line := range strings.Split(result, "\n") {
		if rest, ok := strings.CutPrefix(line, "outcome:"); ok {
			outcome = strings.TrimSpace(rest)
			break
		}
	}
	return outcome, result
}

func TestDispatch_BatchedSend_DeliversWithoutHalting(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:123",
		callerKind: "user",
		userFacing: true,
	}
	outcome, res := runDispatchBatched(t, host, `{"sends": [{"to": "caller:user", "body": "searching..."}]}`, 3)
	if outcome != "delivered-turn-continues" {
		t.Fatalf("outcome=%q; %s", outcome, res)
	}
	if host.sentToCaller != "searching..." {
		t.Errorf("expected delivery, got %q", host.sentToCaller)
	}
	if host.halted {
		t.Fatal("batched dispatch must NOT halt the turn")
	}
	if !host.suppressCleared {
		t.Fatal("batched dispatch must re-enable sink delivery (SendToCaller suppressed it)")
	}
	if !strings.Contains(res, "Turn continues") {
		t.Errorf("result must say the turn continues: %s", res)
	}
}

func TestDispatch_BatchedEmpty_IsNoOp(t *testing.T) {
	host := &mockDispatchHost{currentKey: "cli", callerKind: "user"}
	outcome, res := runDispatchBatched(t, host, `{"sends": []}`, 2)
	if outcome != "no-op" {
		t.Fatalf("outcome=%q; %s", outcome, res)
	}
	if host.halted {
		t.Fatal("batched dispatch({}) must NOT halt the turn")
	}
}

func TestDispatch_ExplicitBatchSizeOne_StillTerminates(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:123",
		callerKind: "user",
		userFacing: true,
	}
	outcome, _ := runDispatchBatched(t, host, `{"sends": [{"to": "caller:user", "body": "done"}]}`, 1)
	if outcome != "turn-terminated" {
		t.Fatalf("outcome=%q", outcome)
	}
	if !host.halted {
		t.Fatal("solo dispatch must halt")
	}
}
