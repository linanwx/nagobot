package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// ReadFileTool reads the contents of a file.
type ReadFileTool struct {
	workspace string
}

// Def returns the tool definition.
func (t *ReadFileTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "read_file",
			Description: "Read the contents of a file at the given path. Relative paths are resolved from workspace root.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the file to read.",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

// readFileArgs are the arguments for read_file.
type readFileArgs struct {
	Path string `json:"path"`
}

// Run executes the tool.
func (t *ReadFileTool) Run(ctx context.Context, args json.RawMessage) string {
	var a readFileArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	path := resolveToolPath(a.Path, t.workspace)
	resolvedPath := absOrOriginal(path)

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

	return string(content)
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

	// Write file
	if err := os.WriteFile(path, []byte(a.Content), 0644); err != nil {
		return fmt.Sprintf("Error: failed to write file: %s: %v", formatResolvedPath(a.Path, resolvedPath), err)
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(a.Content), formatResolvedPath(a.Path, resolvedPath))
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

// ListDirTool lists the contents of a directory.
type ListDirTool struct {
	workspace string
}

// Def returns the tool definition.
func (t *ListDirTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "list_dir",
			Description: "List the contents of a directory. Relative paths are resolved from workspace root.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the directory to list.",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

// listDirArgs are the arguments for list_dir.
type listDirArgs struct {
	Path string `json:"path"`
}

// Run executes the tool.
func (t *ListDirTool) Run(ctx context.Context, args json.RawMessage) string {
	var a listDirArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}
	if strings.TrimSpace(a.Path) == "" {
		return "Error: path is required"
	}

	path := resolveToolPath(a.Path, t.workspace)
	resolvedPath := absOrOriginal(path)

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("Error: directory not found: %s", formatResolvedPath(a.Path, resolvedPath))
		}
		return fmt.Sprintf("Error: failed to stat directory: %s: %v", formatResolvedPath(a.Path, resolvedPath), err)
	}

	if !info.IsDir() {
		return fmt.Sprintf("Error: path is a file, not a directory: %s", formatResolvedPath(a.Path, resolvedPath))
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Sprintf("Error: failed to read directory: %s: %v", formatResolvedPath(a.Path, resolvedPath), err)
	}

	var lines []string
	for _, entry := range entries {
		prefix := "📄 "
		if entry.IsDir() {
			prefix = "📁 "
		}
		lines = append(lines, prefix+entry.Name())
	}

	if len(lines) == 0 {
		return "(empty directory)"
	}

	return strings.Join(lines, "\n")
}
