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
	trashDirName              = ".nagobot-trash"
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

// rewriteRmToTrash rewrites rm commands to mv into a trash directory.
// Returns the rewritten command and the trash dir used, or empty strings if no rewrite needed.
func rewriteRmToTrash(command string) (rewritten string, trashDir string) {
	if !rmPattern.MatchString(command) {
		return "", ""
	}
	trashDir = filepath.Join(os.TempDir(), trashDirName, time.Now().Format("20060102-150405")+"-"+randomHex(3))
	// Rewrite each rm invocation: strip rm and its flags, mv the remaining paths to trash.
	rewritten = rmPattern.ReplaceAllStringFunc(command, func(match string) string {
		// Preserve the leading separator (;, &, |) if present.
		prefix := ""
		trimmed := strings.TrimLeft(match, " ")
		if len(trimmed) > 0 && trimmed[0] != 'r' {
			idx := strings.Index(match, "rm")
			prefix = match[:idx]
		}
		return prefix + "mv "
	})
	// Replace flags like -r, -f, -rf, -fr, --recursive, --force, -i, -I, --interactive, -v, --verbose, -d, --dir
	rewritten = stripRmFlags(rewritten)
	rewritten = strings.TrimRight(rewritten, " ") + " " + shellQuote(trashDir) + "/"
	// Prepend mkdir -p for the trash directory.
	rewritten = "mkdir -p " + shellQuote(trashDir) + " && " + rewritten
	return rewritten, trashDir
}

// stripRmFlags removes rm-specific flags that are invalid for mv.
var rmFlagsPattern = regexp.MustCompile(`\s+(-[rRfivdI]+|--recursive|--force|--interactive(?:=\S+)?|--verbose|--dir|--one-file-system|--no-preserve-root|--preserve-root)(?:\s|$)`)

func stripRmFlags(cmd string) string {
	for {
		next := rmFlagsPattern.ReplaceAllString(cmd, " ")
		if next == cmd {
			break
		}
		cmd = next
	}
	return cmd
}

// shellQuote wraps a string in single quotes for safe shell use.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// Run executes the tool.
func (t *ExecTool) Run(ctx context.Context, args json.RawMessage) string {
	var a execArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	// Rewrite rm → mv to trash directory.
	var trashDir string
	if rewritten, td := rewriteRmToTrash(a.Command); rewritten != "" {
		logger.Info("exec: rewriting rm to trash",
			"original", a.Command,
			"rewritten", rewritten,
			"trashDir", td,
		)
		a.Command = rewritten
		trashDir = td
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
	if trashDir != "" {
		fields["trash_dir"] = trashDir
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
