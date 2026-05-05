package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func newTestExecTool() *ExecTool {
	return NewExecTool("", 5, false)
}

func runExec(t *testing.T, tool *ExecTool, command, confirm string) string {
	t.Helper()
	args := execArgs{Command: command, Confirm: confirm}
	b, _ := json.Marshal(args)
	return tool.Run(context.Background(), b)
}

func TestPipInstallRequiresConfirmation(t *testing.T) {
	tool := newTestExecTool()
	for _, cmd := range []string{
		"pip install lxml",
		"pip3 install lxml --break-system-packages -q",
		"pip3 install --user defusedxml",
		"sudo pip install requests",
		"python3 -m pip install lxml",
		"cd /tmp && pip install foo",
		"bash -c 'pip install foo'",
	} {
		result := runExec(t, tool, cmd, "")
		if !strings.Contains(result, "pip install") || !strings.Contains(result, "uv run") {
			t.Fatalf("cmd %q: expected pip-install prompt with uv suggestion, got: %s", cmd, result)
		}
	}
}

func TestPipInstallWithCorrectHMAC(t *testing.T) {
	tool := newTestExecTool()
	cmd := "pip install --dry-run nonexistent-pkg-xyz-zzz"
	token := tool.computeHMAC(cmd)
	result := runExec(t, tool, cmd, token)
	if strings.Contains(result, "Dangerous command detected") {
		t.Fatalf("expected execution with valid token, got confirmation prompt: %s", result)
	}
}

func TestPipInstallSafeCommandsNotBlocked(t *testing.T) {
	for _, cmd := range []string{
		"uv run --with lxml script.py",
		"uv pip install lxml",
		"pipx install black",
		"npx --package=docx node script.js",
		".venv/bin/pip install lxml",
		"./venv/bin/pip install lxml",
		"echo pip install foo",          // mention as text, not invocation
		"grep 'pip install' README.md",  // searching for the literal string
	} {
		if isPipInstallCommand(cmd) {
			t.Errorf("cmd %q should not be flagged as pip install", cmd)
		}
	}
}

func TestRmRequiresConfirmation(t *testing.T) {
	tool := newTestExecTool()
	result := runExec(t, tool, "rm file.txt", "")
	if !strings.Contains(result, "Dangerous command detected") {
		t.Fatalf("expected confirmation prompt, got: %s", result)
	}
	if !strings.Contains(result, "trash") {
		t.Fatalf("expected trash suggestion, got: %s", result)
	}
}

func TestRmWithCorrectHMAC(t *testing.T) {
	tool := newTestExecTool()
	cmd := "rm /tmp/_nagobot_test_nonexistent_file"
	token := tool.computeHMAC(cmd)
	result := runExec(t, tool, cmd, token)
	if strings.Contains(result, "Dangerous command detected") {
		t.Fatalf("expected execution with valid token, got confirmation prompt: %s", result)
	}
}

func TestRmWithWrongToken(t *testing.T) {
	tool := newTestExecTool()
	result := runExec(t, tool, "rm file.txt", "wrong-token")
	if !strings.Contains(result, "invalid confirmation token") {
		t.Fatalf("expected invalid token error, got: %s", result)
	}
}

func TestRmTokenChangedCommand(t *testing.T) {
	tool := newTestExecTool()
	token := tool.computeHMAC("rm file.txt")
	result := runExec(t, tool, "rm -rf /", token)
	if !strings.Contains(result, "invalid confirmation token") {
		t.Fatalf("expected invalid token error for changed command, got: %s", result)
	}
}

func TestSafeCommandsPassThrough(t *testing.T) {
	cases := []string{"ls", "go build", "echo hello", "cat file.txt"}
	tool := newTestExecTool()
	for _, cmd := range cases {
		result := runExec(t, tool, cmd, "")
		if strings.Contains(result, "Dangerous command detected") {
			t.Errorf("safe command %q triggered confirmation: %s", cmd, result)
		}
	}
}

func TestGrepRmNotTriggered(t *testing.T) {
	tool := newTestExecTool()
	result := runExec(t, tool, "grep rm file.txt", "")
	if strings.Contains(result, "Dangerous command detected") {
		t.Fatalf("grep rm should not trigger confirmation: %s", result)
	}
}

func TestRmInCompoundCommands(t *testing.T) {
	cases := []string{
		"cat x && rm file",
		"cat x & rm file",
		"echo hello; rm file",
		"cat x | rm",
	}
	tool := newTestExecTool()
	for _, cmd := range cases {
		result := runExec(t, tool, cmd, "")
		if !strings.Contains(result, "Dangerous command detected") {
			t.Errorf("compound command %q should trigger confirmation, got: %s", cmd, result)
		}
	}
}

func TestEchoRmNotTriggered(t *testing.T) {
	tool := newTestExecTool()
	result := runExec(t, tool, `echo "rm file.txt"`, "")
	if strings.Contains(result, "Dangerous command detected") {
		t.Fatalf("echo with rm in quotes should not trigger: %s", result)
	}
}

func TestPythonRmTriggered(t *testing.T) {
	tool := newTestExecTool()
	result := runExec(t, tool, `python -c "import os; os.rm('file')"`, "")
	if !strings.Contains(result, "Dangerous command detected") {
		t.Fatalf("python -c with rm should trigger confirmation, got: %s", result)
	}
}

func TestOsascriptRmTriggered(t *testing.T) {
	tool := newTestExecTool()
	result := runExec(t, tool, `osascript -e 'do shell script "rm file"'`, "")
	if !strings.Contains(result, "Dangerous command detected") {
		t.Fatalf("osascript with rm should trigger confirmation, got: %s", result)
	}
}
