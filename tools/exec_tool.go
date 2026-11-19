package tools

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/linanwx/nagobot/prompts"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

const (
	execDefaultTimeoutSeconds = 60
	execOutputMaxChars        = 50000
)

// rmPattern matches `rm` as a direct shell command at the start or after a
// shell operator (|, &, ;). Single `|` and `&` also cover `||` and `&&`.
var rmPattern = regexp.MustCompile(`(?:^|[|&;]\s*)rm(?:\s|$)`)

// subshellRmPattern matches `rm` inside arguments of known sub-shell executors
// (python, osascript, bash, etc.) where quoted rm will actually be executed.
var subshellRmPattern = regexp.MustCompile(`(?:python[23]?|osascript|bash|sh|zsh|ruby|perl|node)\b.*\brm\b`)

// pipInstallPattern matches `pip install` / `pip3 install` and the equivalent
// `python -m pip install` form, at the start or after a shell operator. Catches
// any flag combination including --break-system-packages and --user.
var pipInstallPattern = regexp.MustCompile(`(?:^|[|&;]\s*)(?:sudo\s+)?(?:python3?\s+-m\s+pip|pip3?)\s+install\b`)

// subshellPipInstallPattern catches pip install hidden inside a sub-shell
// executor's arguments (e.g. `bash -c "pip install ..."`).
var subshellPipInstallPattern = regexp.MustCompile(`(?:python[23]?|bash|sh|zsh)\b.*\bpip3?\s+install\b`)

// ExecTool executes shell commands.
type ExecTool struct {
	workspace           string
	defaultTimeout      int
	restrictToWorkspace bool
	hmacKey             []byte
}

// NewExecTool creates an ExecTool with a random HMAC key.
func NewExecTool(workspace string, defaultTimeout int, restrictToWorkspace bool) *ExecTool {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return &ExecTool{
		workspace:           workspace,
		defaultTimeout:      defaultTimeout,
		restrictToWorkspace: restrictToWorkspace,
		hmacKey:             key,
	}
}

// Def returns the tool definition.
func (t *ExecTool) Def() provider.ToolDef {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type": "string",
			},
			"workdir": map[string]any{
				"type": "string",
			},
			"timeout": map[string]any{
				"type": "integer",
			},
			"confirm": map[string]any{
				"type": "string",
			},
		},
		"required": []string{"command"},
	}
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "exec",
			Description: promptToolExec.Describe(params, nil),
			Parameters:  params,
		},
	}
}

// execArgs are the arguments for exec.
type execArgs struct {
	Command string `json:"command" required:"true"`
	Workdir string `json:"workdir,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

// computeHMAC returns a hex-encoded HMAC-SHA256 of the command.
func (t *ExecTool) computeHMAC(command string) string {
	mac := hmac.New(sha256.New, t.hmacKey)
	mac.Write([]byte(command))
	return hex.EncodeToString(mac.Sum(nil))
}

// isRmCommand reports whether the shell command contains an `rm` invocation
// either as a direct shell command or inside a sub-shell executor's arguments.
func isRmCommand(cmd string) bool {
	if !strings.Contains(cmd, "rm") {
		return false
	}
	return rmPattern.MatchString(cmd) || subshellRmPattern.MatchString(cmd)
}

// isPipInstallCommand reports whether the shell command runs `pip install` /
// `pip3 install` (or the python -m pip equivalent) directly or via a sub-shell.
func isPipInstallCommand(cmd string) bool {
	if !strings.Contains(cmd, "pip") {
		return false
	}
	return pipInstallPattern.MatchString(cmd) || subshellPipInstallPattern.MatchString(cmd)
}

// Run executes the tool.
func (t *ExecTool) Run(ctx context.Context, args json.RawMessage) string {
	var a execArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	// An unconfirmed call is one with no token. Trim first: a model that cannot
	// omit a field sends confirm as whitespace, and an untrimmed check would
	// treat that as a token, fail the HMAC compare, and loop on the
	// invalid-token error forever.
	confirm := strings.TrimSpace(a.Confirm)

	// Check for dangerous rm command.
	if isRmCommand(a.Command) {
		if confirm == "" {
			return toolError("exec", promptExecConfirmRm.Render(prompts.Vars{"TOKEN": t.computeHMAC(a.Command)}))
		}
		if !hmac.Equal([]byte(confirm), []byte(t.computeHMAC(a.Command))) {
			return toolError("exec", "invalid confirmation token. The command may have been modified. Retry with confirm left empty (omit it, or pass an empty string) to get a fresh token for the exact command you intend to run.")
		}
	}

	// Check for pip install — pollutes host Python.
	if isPipInstallCommand(a.Command) {
		if confirm == "" {
			return toolError("exec", promptExecConfirmPip.Render(prompts.Vars{"TOKEN": t.computeHMAC(a.Command)}))
		}
		if !hmac.Equal([]byte(confirm), []byte(t.computeHMAC(a.Command))) {
			return toolError("exec", "invalid confirmation token. The command may have been modified. Retry with confirm left empty (omit it, or pass an empty string) to get a fresh token for the exact command you intend to run.")
		}
	}

	timeout := a.Timeout
	if timeout <= 0 {
		if t.defaultTimeout > 0 {
			timeout = t.defaultTimeout
		} else {
			timeout = execDefaultTimeoutSeconds
		}
	}

	return withTimeout(ctx, "exec", time.Duration(timeout)*time.Second, func(ctx context.Context) string {
		return t.run(ctx, a, timeout)
	})
}

func (t *ExecTool) run(ctx context.Context, a execArgs, timeout int) string {
	start := time.Now()
	cmd := exec.CommandContext(ctx, "sh", "-c", a.Command)
	if a.Workdir != "" {
		cmd.Dir = expandPath(a.Workdir)
	} else if t.workspace != "" {
		cmd.Dir = t.workspace
	}

	if t.restrictToWorkspace && t.workspace != "" {
		effectiveDir := cmd.Dir
		if effectiveDir == "" {
			var err error
			effectiveDir, err = os.Getwd()
			if err != nil {
				return fmt.Sprintf("Error: cannot determine working directory: %v", err)
			}
		}
		absDir, err := filepath.Abs(effectiveDir)
		if err != nil {
			return fmt.Sprintf("Error: cannot resolve working directory %q: %v", effectiveDir, err)
		}
		absDir, err = filepath.EvalSymlinks(absDir)
		if err != nil {
			return fmt.Sprintf("Error: cannot resolve symlinks for %q: %v", absDir, err)
		}
		absWorkspace, err := filepath.Abs(t.workspace)
		if err != nil {
			return fmt.Sprintf("Error: cannot resolve workspace %q: %v", t.workspace, err)
		}
		absWorkspace, err = filepath.EvalSymlinks(absWorkspace)
		if err != nil {
			return fmt.Sprintf("Error: cannot resolve symlinks for workspace %q: %v", absWorkspace, err)
		}
		sep := string(filepath.Separator)
		if absDir != absWorkspace && !strings.HasPrefix(absDir+sep, absWorkspace+sep) {
			return fmt.Sprintf("Error: working directory %q is outside workspace %q (restrictToWorkspace is enabled)", effectiveDir, t.workspace)
		}
	}

	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return toolError("exec", fmt.Sprintf("command timed out after %d seconds\nPartial output:\n%s", timeout, string(output)))
	}

	result := string(output)
	result, truncated := truncateWithNotice(result, execOutputMaxChars)
	if truncated {
		logger.Warn("exec output truncated",
			"originalChars", len(output),
			"resultChars", len(result),
			"limit", execOutputMaxChars,
		)
	}

	fields := map[string]any{
		"workdir":     cmd.Dir,
		"duration_ms": time.Since(start).Milliseconds(),
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			fields["exit_code"] = exitErr.ExitCode()
		} else {
			fields["exit_code"] = -1
		}
	} else {
		fields["exit_code"] = 0
	}
	if truncated {
		fields["truncated"] = true
	}
	// Propagate skip_trim from command output YAML header to exec tool header.
	if msg.HasFrontmatterKeyValue(result, "skip_trim", "true") {
		fields["skip_trim"] = true
	}

	return toolResult("exec", fields, result)
}
