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

const readFileDefaultLimit = 2000

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
			Description: "Read a file. Automatically detects file type: text files are returned with line numbers " +
				"and pagination, images are analyzed if the model supports vision or delegated to the imagereader agent, " +
				"and binary files are rejected with an error. " +
				"Use tail to read the last N lines of a text file (offset and limit are ignored when tail is set).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the file to read.",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "Starting line number (1-based). Can be omitted to start from the beginning. Ignored when tail is set.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of lines to return. Can be omitted (reads up to 2000 lines). Ignored when tail is set.",
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
			return toolError("read_file", fmt.Sprintf("file not found: %s", formatResolvedPath(a.Path, resolvedPath)))
		}
		return toolError("read_file", fmt.Sprintf("failed to stat file: %s: %v", formatResolvedPath(a.Path, resolvedPath), err))
	}

	if info.IsDir() {
		return toolError("read_file", fmt.Sprintf("path is a directory, not a file: %s", formatResolvedPath(a.Path, resolvedPath)))
	}

	// Detect file type and dispatch accordingly.
	fileType, mimeType := DetectFileType(path)
	switch fileType {
	case FileTypeImage:
		return t.handleImage(ctx, resolvedPath, mimeType, info.Size())
	case FileTypeBinary:
		return toolError("read_file", fmt.Sprintf("binary file (%s), cannot read as text: %s", mimeType, resolvedPath))
	default:
		return t.handleText(a, path, resolvedPath)
	}
}

// handleImage returns image data for vision-capable models or delegation guidance.
// absPath must be an absolute path (used for both display and media markers).
func (t *ReadFileTool) handleImage(ctx context.Context, absPath, mimeType string, size int64) string {
	fields := map[string]any{"path": absPath, "type": mimeType, "size": size}
	rt := RuntimeContextFrom(ctx)
	if !rt.SupportsVision {
		return toolResult("read_file", fields,
			"This is an image file. You cannot view images directly. "+
				"Use the spawn_thread tool to delegate to the 'imagereader' agent, "+
				"passing the original user message as the task.")
	}
	return toolResult("read_file", fields, fmt.Sprintf("<<media:%s:%s>>", mimeType, absPath))
}

// handleText reads a text file with line-based pagination.
// filePath is the workspace-resolved path for reading; absPath is the absolute path for display.
func (t *ReadFileTool) handleText(a readFileArgs, filePath, absPath string) string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return toolError("read_file", fmt.Sprintf("failed to read file: %s: %v", formatResolvedPath(a.Path, absPath), err))
	}
	if len(content) == 0 {
		return toolError("read_file", fmt.Sprintf("file exists but is empty: %s", absPath))
	}

	allLines := strings.Split(string(content), "\n")
	totalLines := len(allLines)

	var startIdx, endIdx int

	if a.Tail > 0 {
		startIdx = totalLines - a.Tail
		if startIdx < 0 {
			startIdx = 0
		}
		endIdx = totalLines
	} else {
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
			return toolError("read_file", fmt.Sprintf("offset %d is beyond end of file (%d lines)", offset, totalLines))
		}
		endIdx = startIdx + limit
		if endIdx > totalLines {
			endIdx = totalLines
		}
	}

	fields := map[string]any{
		"path":  absPath,
		"lines": fmt.Sprintf("%d-%d", startIdx+1, endIdx),
		"total": totalLines,
	}
	if endIdx < totalLines {
		fields["next_offset"] = endIdx + 1
	}

	var sb strings.Builder
	for i := startIdx; i < endIdx; i++ {
		fmt.Fprintf(&sb, "%d\t%s\n", i+1, allLines[i])
	}

	return toolResult("read_file", fields, sb.String())
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
		return toolError("write_file", fmt.Sprintf("failed to create parent directory: %s: %v", formatResolvedPath(dir, resolvedDir), err))
	}

	// Write file (overwrite)
	if err := os.WriteFile(path, []byte(a.Content), 0644); err != nil {
		return toolError("write_file", fmt.Sprintf("failed to write file: %s: %v", formatResolvedPath(a.Path, resolvedPath), err))
	}

	return toolResult("write_file", map[string]any{
		"path":  resolvedPath,
		"bytes": len(a.Content),
	}, "")
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
			Description: "Edit a file by replacing specific text. Relative paths are resolved from workspace root. The old_text must match exactly (trailing whitespace differences are tolerated). Use replace_all to replace every occurrence (e.g. renaming a variable).",
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
					"replace_all": map[string]any{
						"type":        "boolean",
						"description": "Replace all occurrences instead of requiring a unique match. Defaults to false.",
					},
				},
				"required": []string{"path", "old_text", "new_text"},
			},
		},
	}
}

// editFileArgs are the arguments for edit_file.
type editFileArgs struct {
	Path       string `json:"path"`
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// normalizeTrailingWS strips trailing spaces/tabs from each line for fuzzy matching.
func normalizeTrailingWS(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Join(lines, "\n")
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
			return toolError("edit_file", fmt.Sprintf("file not found: %s", formatResolvedPath(a.Path, resolvedPath)))
		}
		return toolError("edit_file", fmt.Sprintf("failed to read file: %s: %v", formatResolvedPath(a.Path, resolvedPath), err))
	}

	contentStr := string(content)
	displayPath := formatResolvedPath(a.Path, resolvedPath)

	// Try exact match first.
	count := strings.Count(contentStr, a.OldText)

	if count == 0 {
		// Fuzzy fallback: normalize trailing whitespace on both sides.
		normContent := normalizeTrailingWS(contentStr)
		normOld := normalizeTrailingWS(a.OldText)
		normCount := strings.Count(normContent, normOld)

		if normCount == 0 {
			return toolError("edit_file", fmt.Sprintf("text not found in file: %q (path: %s)", a.OldText, displayPath))
		}
		if normCount > 1 && !a.ReplaceAll {
			return toolError("edit_file", fmt.Sprintf("text appears %d times in file (path: %s); match must be unique. Provide more context or use replace_all.", normCount, displayPath))
		}

		// Find the corresponding region in the original content and replace.
		var newContent string
		if a.ReplaceAll {
			newContent = normalizedReplaceAll(contentStr, normOld, a.NewText)
		} else {
			newContent = normalizedReplace(contentStr, normOld, a.NewText)
		}

		if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
			return toolError("edit_file", fmt.Sprintf("failed to write file: %s: %v", displayPath, err))
		}
		n := normCount
		if !a.ReplaceAll {
			n = 1
		}
		return toolResult("edit_file", map[string]any{
			"path":         displayPath,
			"replacements": n,
			"fuzzy":        true,
		}, "")
	}

	if count > 1 && !a.ReplaceAll {
		return toolError("edit_file", fmt.Sprintf("text appears %d times in file (path: %s); match must be unique. Provide more context or use replace_all.", count, displayPath))
	}

	var newContent string
	if a.ReplaceAll {
		newContent = strings.ReplaceAll(contentStr, a.OldText, a.NewText)
	} else {
		newContent = strings.Replace(contentStr, a.OldText, a.NewText, 1)
	}

	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return toolError("edit_file", fmt.Sprintf("failed to write file: %s: %v", displayPath, err))
	}

	return toolResult("edit_file", map[string]any{
		"path":         displayPath,
		"replacements": count,
	}, "")
}

// normToOrigPos maps a character position in normalized text back to the
// corresponding position in the original text. Since normalization only
// trims trailing whitespace per line, positions within a line are identical;
// only inter-line offsets differ.
func normToOrigPos(origLines, normLines []string, normPos int) int {
	normOff := 0
	origOff := 0
	for i := range normLines {
		nlLen := len(normLines[i])
		if normPos <= normOff+nlLen {
			return origOff + (normPos - normOff)
		}
		normOff += nlLen + 1 // +1 for \n
		origOff += len(origLines[i]) + 1
	}
	return origOff
}

// normalizedReplace replaces the first occurrence of normOld in content,
// where matching is done on trailing-whitespace-normalized text but the
// replacement is applied to the original content.
func normalizedReplace(content, normOld, newText string) string {
	origLines := strings.Split(content, "\n")
	normLines := make([]string, len(origLines))
	for i, l := range origLines {
		normLines[i] = strings.TrimRight(l, " \t")
	}
	normContent := strings.Join(normLines, "\n")

	normIdx := strings.Index(normContent, normOld)
	if normIdx < 0 {
		return content
	}

	origStart := normToOrigPos(origLines, normLines, normIdx)
	origEnd := normToOrigPos(origLines, normLines, normIdx+len(normOld))
	return content[:origStart] + newText + content[origEnd:]
}

// normalizedReplaceAll replaces all occurrences using normalized matching.
// Matches are found in normalized text and mapped back to original positions.
// Replacements proceed back-to-front to preserve earlier positions.
func normalizedReplaceAll(content, normOld, newText string) string {
	origLines := strings.Split(content, "\n")
	normLines := make([]string, len(origLines))
	for i, l := range origLines {
		normLines[i] = strings.TrimRight(l, " \t")
	}
	normContent := strings.Join(normLines, "\n")

	// Collect all match positions in normalized text.
	var matches []int
	pos := 0
	for {
		idx := strings.Index(normContent[pos:], normOld)
		if idx < 0 {
			break
		}
		matches = append(matches, pos+idx)
		pos += idx + len(normOld)
	}
	if len(matches) == 0 {
		return content
	}

	// Replace back-to-front to keep earlier positions valid.
	result := content
	for i := len(matches) - 1; i >= 0; i-- {
		origStart := normToOrigPos(origLines, normLines, matches[i])
		origEnd := normToOrigPos(origLines, normLines, matches[i]+len(normOld))
		result = result[:origStart] + newText + result[origEnd:]
	}
	return result
}
