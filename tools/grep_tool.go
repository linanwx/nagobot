package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

// GrepTool searches file contents using regex patterns.
type GrepTool struct {
	workspace string
}

// Def returns the tool definition.
func (t *GrepTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "grep",
			Description: "Search file contents using a regular expression pattern. Uses ripgrep (rg) if available, otherwise falls back to grep -rn. Returns matching lines with file paths and line numbers.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "The regular expression pattern to search for.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "The directory or file to search in. Defaults to workspace root.",
					},
					"include": map[string]any{
						"type":        "string",
						"description": "Glob pattern to filter files, e.g. \"*.go\".",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Maximum number of matches to return. Defaults to 50.",
					},
					"context_lines": map[string]any{
						"type":        "integer",
						"description": "Number of context lines before and after each match. Defaults to 0.",
					},
					"case_insensitive": map[string]any{
						"type":        "boolean",
						"description": "Ignore case when matching.",
					},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

type grepArgs struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Include         string `json:"include,omitempty"`
	MaxResults      int    `json:"max_results,omitempty"`
	ContextLines    int    `json:"context_lines,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
}

// Run executes the tool.
func (t *GrepTool) Run(ctx context.Context, args json.RawMessage) string {
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
			return fmt.Sprintf("No matches found for pattern: %s", a.Pattern)
		}
		if output != "" {
			return fmt.Sprintf("Error: %s", output)
		}
		return fmt.Sprintf("Error: %v", err)
	}

	if output == "" {
		return fmt.Sprintf("No matches found for pattern: %s", a.Pattern)
	}

	// Truncate to max_results lines
	lines := strings.Split(output, "\n")
	if len(lines) > maxResults {
		output = strings.Join(lines[:maxResults], "\n")
		output += fmt.Sprintf("\n\n(truncated: showing %d of %d lines)", maxResults, len(lines))
	}

	return output
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

func (t *GrepTool) buildGrepArgs(a grepArgs, searchPath string) []string {
	args := []string{"-rn"}
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
