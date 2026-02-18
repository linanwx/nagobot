package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

func absOrOriginal(path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return absPath
}

func formatResolvedPath(input, resolved string) string {
	return fmt.Sprintf("%s (resolved: %s)", input, resolved)
}

const readFileDefaultLimit = 100

// ReadFileTool reads the contents of a file with line-based pagination.
type ReadFileTool struct {
	workspace string
}

// Def returns the tool definition.
func (t *ReadFileTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name: "read_file",
			Description: "Read lines from a file. Returns up to 100 lines starting from offset (default 1). " +
				"If the file has more lines than the limit, a notice is appended showing total line count " +
				"so you can make follow-up calls with offset to read the rest. " +
				"Use tail to read the last N lines of the file (offset and limit are ignored when tail is set).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the file to read.",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "Starting line number (1-based). Defaults to 1. Ignored when tail is set.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of lines to return. Defaults to 100. Ignored when tail is set.",
					},
					"tail": map[string]any{
						"type":        "integer",
						"description": "Read the last N lines of the file. When set, offset and limit are ignored.",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

// readFileArgs are the arguments for read_file.
type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Tail   int    `json:"tail,omitempty"`
}

// Run executes the tool.
func (t *ReadFileTool) Run(ctx context.Context, args json.RawMessage) string {
	var a readFileArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	path := resolveToolPath(a.Path, t.workspace)
	resolvedPath := absOrOriginal(path)
	logger.Debug("read_file resolved path", "inputPath", a.Path, "resolvedPath", resolvedPath)

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("Error: file not found: %s", formatResolvedPath(a.Path, resolvedPath))
		}
		return fmt.Sprintf("Error: failed to stat file: %s: %v", formatResolvedPath(a.Path, resolvedPath), err)
	}

	if info.IsDir() {
		return fmt.Sprintf("Error: path is a directory, not a file: %s", formatResolvedPath(a.Path, resolvedPath))
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Error: failed to read file: %s: %v", formatResolvedPath(a.Path, resolvedPath), err)
	}
	if len(content) == 0 {
		return fmt.Sprintf("Error: file exists but is empty: %s", resolvedPath)
	}

	allLines := strings.Split(string(content), "\n")
	totalLines := len(allLines)

	var startIdx, endIdx int

	if a.Tail > 0 {
		// tail mode: read last N lines
		startIdx = totalLines - a.Tail
		if startIdx < 0 {
			startIdx = 0
		}
		endIdx = totalLines
	} else {
		// normal mode: offset + limit
		offset := a.Offset
		if offset <= 0 {
			offset = 1
		}
		limit := a.Limit
		if limit <= 0 {
			limit = readFileDefaultLimit
		}

		startIdx = offset - 1
		if startIdx >= totalLines {
			return fmt.Sprintf("[File has %d lines. Offset %d is beyond end of file.]", totalLines, offset)
		}
		endIdx = startIdx + limit
		if endIdx > totalLines {
			endIdx = totalLines
		}
	}

	var sb strings.Builder
	if a.Tail <= 0 && endIdx < totalLines {
		fmt.Fprintf(&sb, "[Showing lines %d-%d of %d total. Use offset=%d to read more.]\n\n",
			startIdx+1, endIdx, totalLines, endIdx+1)
	}
	for i := startIdx; i < endIdx; i++ {
		fmt.Fprintf(&sb, "%d\t%s\n", i+1, allLines[i])
	}

	return sb.String()
}

// WriteFileTool writes content to a file.
type WriteFileTool struct {
	workspace string
}

// Def returns the tool definition.
func (t *WriteFileTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "write_file",
			Description: "Write content to a file at the given path. Relative paths are resolved from workspace root. Creates parent directories if needed.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the file to write.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to write to the file.",
					},
				},
				"required": []string{"path", "content"},
			},
		},
	}
}

// writeFileArgs are the arguments for write_file.
type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Run executes the tool.
func (t *WriteFileTool) Run(ctx context.Context, args json.RawMessage) string {
	var a writeFileArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	path := resolveToolPath(a.Path, t.workspace)
	resolvedPath := absOrOriginal(path)

	// Create parent directories
	dir := filepath.Dir(path)
	resolvedDir := absOrOriginal(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Sprintf("Error: failed to create parent directory: %s: %v", formatResolvedPath(dir, resolvedDir), err)
	}

	// Write file (overwrite)
	if err := os.WriteFile(path, []byte(a.Content), 0644); err != nil {
		return fmt.Sprintf("Error: failed to write file: %s: %v", formatResolvedPath(a.Path, resolvedPath), err)
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(a.Content), formatResolvedPath(a.Path, resolvedPath))
}

// AppendFileTool appends content to a file.
type AppendFileTool struct {
	workspace string
}

// Def returns the tool definition.
func (t *AppendFileTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "append_file",
			Description: "Append content to a file at the given path. Relative paths are resolved from workspace root. Creates parent directories if needed.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the file to append.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to append to the file.",
					},
				},
				"required": []string{"path", "content"},
			},
		},
	}
}

type appendFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Run executes the tool.
func (t *AppendFileTool) Run(ctx context.Context, args json.RawMessage) string {
	var a appendFileArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	path := resolveToolPath(a.Path, t.workspace)
	resolvedPath := absOrOriginal(path)

	dir := filepath.Dir(path)
	resolvedDir := absOrOriginal(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Sprintf("Error: failed to create parent directory: %s: %v", formatResolvedPath(dir, resolvedDir), err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Sprintf("Error: failed to open file for append: %s: %v", formatResolvedPath(a.Path, resolvedPath), err)
	}
	defer f.Close()

	if _, err := f.WriteString(a.Content); err != nil {
		return fmt.Sprintf("Error: failed to append file: %s: %v", formatResolvedPath(a.Path, resolvedPath), err)
	}

	return fmt.Sprintf("Successfully appended %d bytes to %s", len(a.Content), formatResolvedPath(a.Path, resolvedPath))
}

// EditFileTool edits a file by replacing text.
type EditFileTool struct {
	workspace string
}

// Def returns the tool definition.
func (t *EditFileTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "edit_file",
			Description: "Edit a file by replacing specific text. Relative paths are resolved from workspace root. The old_text must match exactly (including whitespace).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the file to edit.",
					},
					"old_text": map[string]any{
						"type":        "string",
						"description": "The exact text to find and replace.",
					},
					"new_text": map[string]any{
						"type":        "string",
						"description": "The text to replace with.",
					},
				},
				"required": []string{"path", "old_text", "new_text"},
			},
		},
	}
}

// editFileArgs are the arguments for edit_file.
type editFileArgs struct {
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// Run executes the tool.
func (t *EditFileTool) Run(ctx context.Context, args json.RawMessage) string {
	var a editFileArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	path := resolveToolPath(a.Path, t.workspace)
	resolvedPath := absOrOriginal(path)

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("Error: file not found: %s", formatResolvedPath(a.Path, resolvedPath))
		}
		return fmt.Sprintf("Error: failed to read file: %s: %v", formatResolvedPath(a.Path, resolvedPath), err)
	}

	contentStr := string(content)
	count := strings.Count(contentStr, a.OldText)
	if count == 0 {
		return fmt.Sprintf("Error: text not found in file: %q (path: %s)", a.OldText, formatResolvedPath(a.Path, resolvedPath))
	}
	if count > 1 {
		return fmt.Sprintf("Error: text appears %d times in file (path: %s); match must be unique. Provide more context.", count, formatResolvedPath(a.Path, resolvedPath))
	}

	newContent := strings.Replace(contentStr, a.OldText, a.NewText, 1)
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return fmt.Sprintf("Error: failed to write file: %s: %v", formatResolvedPath(a.Path, resolvedPath), err)
	}

	return fmt.Sprintf("Successfully edited %s", formatResolvedPath(a.Path, resolvedPath))
}
