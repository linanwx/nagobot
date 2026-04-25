package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/linanwx/nagobot/thread/msg"
)

type mockDispatchHost struct {
	currentKey    string
	callerKind    msg.CallerKind // "user" / "session" / "system" / "" (none)
	callerKey     string         // non-empty only when callerKind == "session"
	sinkLabel     string
	userFacing    bool
	agents        map[string]bool
	sessions      map[string]bool
	halted        bool
	sentToCaller  string
	sentToUser    string
	subagentCalls []subagentCall
	forkCalls     []subagentCall
	wokeSessions  []wakeCall
	failAgent     string // when non-empty, create/wake of this agent returns error
}

type subagentCall struct {
	Agent, TaskID, Body string
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
func (m *mockDispatchHost) CreateOrWakeSubagent(_ context.Context, agent, taskID, body string) (string, string, error) {
	if m.failAgent != "" && agent == m.failAgent {
		return "", "", fmt.Errorf("simulated failure")
	}
	m.subagentCalls = append(m.subagentCalls, subagentCall{agent, taskID, body})
	key := m.currentKey + ":threads:" + taskID
	note := "created"
	if m.sessions[key] {
		note = "resumed"
	}
	return key, note, nil
}
func (m *mockDispatchHost) CreateOrWakeFork(_ context.Context, agent, taskID, body string) (string, string, error) {
	if m.failAgent != "" && agent == m.failAgent {
		return "", "", fmt.Errorf("simulated failure")
	}
	m.forkCalls = append(m.forkCalls, subagentCall{agent, taskID, body})
	key := m.currentKey + ":fork:" + taskID
	note := "forked-from:" + m.currentKey
	if m.sessions[key] {
		note = "resumed"
	}
	return key, note, nil
}
func (m *mockDispatchHost) WakeSession(_ context.Context, sessionKey, body string) error {
	m.wokeSessions = append(m.wokeSessions, wakeCall{sessionKey, body})
	return nil
}
func (m *mockDispatchHost) SignalHalt() { m.halted = true }

// runDispatch is a test helper that invokes the tool and returns the parsed
// outcome field plus the full result string for assertions.
func runDispatch(t *testing.T, host *mockDispatchHost, argsJSON string) (outcome, result string) {
	t.Helper()
	tool := NewDispatchTool(host)
	result = tool.Run(context.Background(), json.RawMessage(argsJSON))

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

// Hint fires when caller is the channel user and dispatch sends to caller:user —
// the assistant message in this turn would already auto-deliver, so this is redundant.
func TestDispatch_CallerUser_HintsRedundantWhenCallerIsUser(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "user",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "caller:user", "body": "hi"}]}`)
	if !strings.Contains(res, "redundant") {
		t.Errorf("expected redundant-delivery hint; got: %s", res)
	}
	if strings.Contains(res, "had no to=user entry") {
		t.Errorf("noUserReminder must NOT fire when reach-user path is taken; got: %s", res)
	}
}

// Hint also fires for to=user when caller is the channel user.
func TestDispatch_User_HintsRedundantWhenCallerIsUser(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "cli",
		callerKind: "user",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "user", "body": "hi"}]}`)
	if !strings.Contains(res, "redundant") {
		t.Errorf("expected redundant-delivery hint; got: %s", res)
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
func TestDispatch_ReminderWhenCallerSession(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:42",
		callerKind: "session",
		callerKey:  "cli",
		userFacing: true,
	}
	_, res := runDispatch(t, host, `{"sends": [{"to": "caller:session", "body": "hi"}]}`)
	if !strings.Contains(res, "no to=user entry") {
		t.Errorf("noUserReminder must fire when caller is a peer session; got:\n%s", res)
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
		`{"sends": [{"to": "subagent", "agent": "search", "task_id": "bg-check", "body": "查 X"}]}`)
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
		`{"sends": [{"to": "subagent", "agent": "nonexistent", "task_id": "x", "body": "go"}]}`)
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
		`{"sends": [{"to": "subagent", "task_id": "bg-check", "body": "go"}]}`)
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
		`{"sends": [{"to": "subagent", "agent": "s", "task_id": "BAD ID!", "body": "x"}]}`)
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
		`{"sends": [{"to": "fork", "agent": "analyst", "task_id": "hypo-a", "body": "explore"}]}`)
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

func TestDispatch_ForkNested(t *testing.T) {
	host := &mockDispatchHost{
		currentKey: "telegram:1:fork:a",
		callerKind: "session",
		callerKey:  "telegram:1",
		agents:     map[string]bool{"analyst": true},
	}
	_, res := runDispatch(t, host,
		`{"sends": [{"to": "fork", "agent": "analyst", "task_id": "b", "body": "deeper"}]}`)
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
		`{"sends": [{"to": "session", "session_key": "telegram:2", "body": "ping"}]}`)
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
		`{"sends": [{"to": "session", "session_key": "telegram:999", "body": "ping"}]}`)
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
		`{"sends": [{"to": "session", "session_key": "telegram:1", "body": "me"}]}`)
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
			{"to": "subagent", "agent": "search", "task_id": "bg", "body": "查"},
			{"to": "fork", "agent": "analyst", "task_id": "hypo", "body": "branch"},
			{"to": "session", "session_key": "telegram:2", "body": "sync"}
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
			{"to": "subagent", "agent": "s", "task_id": "x", "body": "1"},
			{"to": "subagent", "agent": "s", "task_id": "x", "body": "2"}
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
			{"to": "subagent", "agent": "search", "task_id": "ok", "body": "a"},
			{"to": "subagent", "agent": "broken", "task_id": "bad", "body": "b"}
		]}`)
	if !strings.Contains(res, "partial-failure") {
		t.Errorf("expected partial-failure, got: %s", res)
	}
	if !host.halted {
		t.Error("expected halt after execution attempted (successes unrecoverable)")
	}
}
