package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

const (
	execDefaultTimeoutSeconds = 60
	execOutputMaxChars        = 50000
)

// rmPattern matches standalone rm commands (not as part of another word).
// Matches: rm, rm -rf, rm -f, etc. Does NOT match: cargo, gorm, xrm.
var rmPattern = regexp.MustCompile(`(?:^|[;&|]\s*)rm\s`)

// ExecTool executes shell commands.
type ExecTool struct {
	workspace           string
	defaultTimeout      int
	restrictToWorkspace bool
}

// Def returns the tool definition.
func (t *ExecTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "exec",
			Description: "Execute a shell command and return its output. Use for running programs, scripts, git commands, etc.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The shell command to execute.",
					},
					"workdir": map[string]any{
						"type":        "string",
						"description": "Optional working directory. If omitted, runs in the workspace root.",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Optional timeout in seconds. If omitted, uses the system-configured default.",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

// execArgs are the arguments for exec.
type execArgs struct {
	Command string `json:"command"`
	Workdir string `json:"workdir,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// Run executes the tool.
func (t *ExecTool) Run(ctx context.Context, args json.RawMessage) string {
	var a execArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	// Block rm commands — prompt the LLM to use safer alternatives.
	if rmPattern.MatchString(a.Command) {
		logger.Info("exec: blocked rm command", "command", a.Command)
		return toolError("exec", "rm command is not allowed. Try use other way (for example, on macOS use `trash` instead).")
	}

	timeout := a.Timeout
	if timeout <= 0 {
		if t.defaultTimeout > 0 {
			timeout = t.defaultTimeout
		} else {
			timeout = execDefaultTimeoutSeconds
		}
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", a.Command)
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
	if execCtx.Err() == context.DeadlineExceeded {
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
		"command": a.Command,
		"workdir": cmd.Dir,
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

	return toolResult("exec", fields, result)
}
