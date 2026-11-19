package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"os/exec"
	"strings"
)

// GrepTool searches file contents using regex patterns.
type GrepTool struct {
	workspace string
}

// Def returns the tool definition.
func (t *GrepTool) Def() provider.ToolDef {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type": "string",
			},
			"path": map[string]any{
				"type": "string",
			},
			"include": map[string]any{
				"type": "string",
			},
			"max_results": map[string]any{
				"type": "integer",
			},
			"context_lines": map[string]any{
				"type": "integer",
			},
			"case_insensitive": map[string]any{
				"type": "boolean",
			},
		},
		"required": []string{"pattern"},
	}
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "grep",
			Description: promptToolGrep.Describe(params, nil),
			Parameters:  params,
		},
	}
}

type grepArgs struct {
	Pattern         string `json:"pattern" required:"true"`
	Path            string `json:"path,omitempty"`
	Include         string `json:"include,omitempty"`
	MaxResults      int    `json:"max_results,omitempty"`
	ContextLines    int    `json:"context_lines,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
}

// Run executes the tool.
func (t *GrepTool) Run(ctx context.Context, args json.RawMessage) string {
	return withTimeout(ctx, "grep", grepToolTimeout, func(ctx context.Context) string {
		return t.run(ctx, args)
	})
}

func (t *GrepTool) run(ctx context.Context, args json.RawMessage) string {
	var a grepArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	if a.Pattern == "" {
		return "Error: pattern is required"
	}

	searchPath := t.workspace
	if a.Path != "" {
		searchPath = resolveToolPath(a.Path, t.workspace)
	}
	if searchPath == "" {
		searchPath = "."
	}

	maxResults := a.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	var cmdArgs []string
	var cmdName string

	if rgPath, err := exec.LookPath("rg"); err == nil {
		cmdName = rgPath
		cmdArgs = t.buildRgArgs(a, searchPath)
	} else {
		cmdName = "grep"
		cmdArgs = t.buildGrepArgs(a, searchPath)
	}

	logger.Debug("grep tool exec", "cmd", cmdName, "args", cmdArgs)

	cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
	out, err := cmd.CombinedOutput()
	output := strings.TrimRight(string(out), "\n")

	if err != nil {
		// Exit code 1 means no matches for both rg and grep
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return toolResult("grep", map[string]any{
				"pattern": a.Pattern,
				"path":    searchPath,
				"lines":   0,
			}, "No matches found.")
		}
		if output != "" {
			return toolError("grep", output)
		}
		return toolError("grep", fmt.Sprintf("%v", err))
	}

	if output == "" {
		return toolResult("grep", map[string]any{
			"pattern": a.Pattern,
			"path":    searchPath,
			"lines":   0,
		}, "No matches found.")
	}

	// Truncate to max_results lines. The count reported back is `lines`, not
	// `results`: with context_lines > 0 a single match spans several output
	// lines plus a separator, so calling this a match count overstated it by
	// roughly the context factor.
	lines := strings.Split(output, "\n")
	fields := map[string]any{
		"pattern": a.Pattern,
		"path":    searchPath,
		"lines":   len(lines),
	}
	if len(lines) > maxResults {
		fields["lines"] = maxResults
		fields["total"] = len(lines)
		fields["truncated"] = true
		output = strings.Join(lines[:maxResults], "\n")
	}

	return toolResult("grep", fields, output)
}

func (t *GrepTool) buildRgArgs(a grepArgs, searchPath string) []string {
	args := []string{"--no-heading", "--line-number", "--color", "never", "--no-config"}
	if a.Include != "" {
		args = append(args, "--glob", a.Include)
	}
	if a.CaseInsensitive {
		args = append(args, "-i")
	}
	if a.ContextLines > 0 {
		args = append(args, "-C", fmt.Sprintf("%d", a.ContextLines))
	}
	args = append(args, a.Pattern, searchPath)
	return args
}

// buildGrepArgs builds the fallback command for hosts without ripgrep.
//
// `-E` is load-bearing, not a style choice. Bare grep is BRE, where `|` and
// `(` are LITERAL characters — so the alternation patterns this tool's
// description and the session-ops skill both tell the model to write
// (`护照|passport`) matched nothing at all on such a host, while the same
// pattern under rg matched dozens of lines. The failure was silent and
// indistinguishable from "the fact was never mentioned", which is the exact
// conclusion the model then drew. ERE is the closest common dialect to
// ripgrep's, so the two backends now answer the same question.
func (t *GrepTool) buildGrepArgs(a grepArgs, searchPath string) []string {
	args := []string{"-rnE"}
	if a.Include != "" {
		args = append(args, "--include="+a.Include)
	}
	if a.CaseInsensitive {
		args = append(args, "-i")
	}
	if a.ContextLines > 0 {
		args = append(args, "-C", fmt.Sprintf("%d", a.ContextLines))
	}
	args = append(args, a.Pattern, searchPath)
	return args
}
