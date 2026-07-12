package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"os"
	"path/filepath"
	"strings"
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

// readFileMaxBytes bounds read_file text output by bytes (in addition to the
// line limit) so files with few but very long lines cannot blow the context.
const readFileMaxBytes = 50 * 1024

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
				"audio files are analyzed if the model supports audio or delegated to the audioreader agent, " +
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
	Path   string `json:"path" required:"true"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Tail   int    `json:"tail,omitempty"`
}

// Run executes the tool.
func (t *ReadFileTool) Run(ctx context.Context, args json.RawMessage) string {
	return withTimeout(ctx, "read_file", fileToolTimeout, func(ctx context.Context) string {
		return t.run(ctx, args)
	})
}

func (t *ReadFileTool) run(ctx context.Context, args json.RawMessage) string {
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
	case FileTypeAudio:
		return t.handleAudio(ctx, resolvedPath, mimeType, info.Size())
	case FileTypePDF:
		return t.handlePDF(ctx, resolvedPath, mimeType, info.Size())
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
		if !rt.ImageReaderConfigured {
			return toolResult("read_file", fields,
				"This is an image file. Your current model does not support vision, "+
					"and the 'imagereader' agent is not configured. "+
					"To enable image reading, configure a vision-capable model or set up an imagereader agent.")
		}
		return toolResult("read_file", fields,
			"This is an image file. You cannot view images directly. "+
				"Delegate to the imagereader subagent: call dispatch with to=subagent, agent='imagereader', "+
				"a descriptive task_id (e.g. 'read-image-<short-name>'), and a body that includes this image's "+
				"file path followed by the user's question/context.\n"+
				"path: "+absPath+"\n"+
				"imagereader reads the image from that path itself; the body MUST contain the path or it cannot proceed.")
	}
	return toolResult("read_file", fields, fmt.Sprintf("<<media:%s:%s>>", mimeType, absPath))
}

// handleAudio returns audio data for audio-capable models or delegation guidance.
func (t *ReadFileTool) handleAudio(ctx context.Context, absPath, mimeType string, size int64) string {
	fields := map[string]any{"path": absPath, "type": mimeType, "size": size}
	rt := RuntimeContextFrom(ctx)
	if !rt.SupportsAudio {
		if !rt.AudioReaderConfigured {
			return toolResult("read_file", fields,
				"This is an audio file. Your current model does not support audio, "+
					"and the 'audioreader' agent is not configured. "+
					"To enable audio reading, configure an audio-capable model or set up an audioreader agent.")
		}
		return toolResult("read_file", fields,
			"This is an audio file. You cannot listen to audio directly. "+
				"Use dispatch with to=subagent, agent='audioreader', and pass the audio file path as the body. "+
				"Pick a descriptive task_id (e.g. 'read-audio-<short-name>').")
	}
	return toolResult("read_file", fields, fmt.Sprintf("<<media:%s:%s>>", mimeType, absPath))
}

// handlePDF returns extraction guidance. PDFs are not read natively — the model
// must extract the document first (render pages to images, or extract text) and
// read the result, or use an MCP PDF reader.
func (t *ReadFileTool) handlePDF(ctx context.Context, absPath, mimeType string, size int64) string {
	fields := map[string]any{"path": absPath, "type": mimeType, "size": size}
	return toolResult("read_file", fields,
		"This is a PDF document. PDFs are not read natively. Extract it first, then read the result: "+
			"use the exec tool to render pages to images (e.g. pdftoppm / pdftocairo / ImageMagick) and read_file "+
			"those images (vision is supported), or extract text (e.g. pdftotext). If no such tool is available "+
			"on this host, say so instead of guessing the contents.")
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

	// Byte cap, applied within the line window. A single line over the limit
	// means the content likely isn't plain text (minified/binary) — error out
	// without dumping it. Otherwise stop at the byte budget and paginate.
	acc := 0
	effEnd := startIdx
	for i := startIdx; i < endIdx; i++ {
		lineBytes := len(allLines[i])
		if i == startIdx && lineBytes > readFileMaxBytes {
			return toolError("read_file", fmt.Sprintf(
				"line %d is %.1fKB, exceeds the %dKB read limit: %s. The file may not be plain text "+
					"(e.g. minified or binary). Read it another way — extract/convert it first, or use exec "+
					"with a targeted command.",
				i+1, float64(lineBytes)/1024, readFileMaxBytes/1024, absPath))
		}
		if acc+lineBytes > readFileMaxBytes {
			break
		}
		acc += lineBytes
		effEnd = i + 1
	}

	fields := map[string]any{
		"path":  absPath,
		"lines": fmt.Sprintf("%d-%d", startIdx+1, effEnd),
		"total": totalLines,
	}
	if effEnd < totalLines {
		fields["next_offset"] = effEnd + 1
	}
	if effEnd < endIdx {
		fields["truncated"] = fmt.Sprintf("%dKB byte limit", readFileMaxBytes/1024)
	}

	var sb strings.Builder
	for i := startIdx; i < effEnd; i++ {
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
						"description": "The content to write to the file. Always required — the file is overwritten with exactly this text. Pass an empty string to create an empty file.",
					},
				},
				"required": []string{"path", "content"},
			},
		},
	}
}

// writeFileArgs are the arguments for write_file.
//
// Content is a *string, not a string, and that is load-bearing: write_file
// overwrites, so a dropped `content` key must NOT decode to "" and silently
// truncate the target file to zero bytes. A pointer distinguishes "key absent"
// (nil → rejected by the required check) from "key present and empty" (""" →
// a legitimate empty-file write). See the tool argument contract in tools.go.
type writeFileArgs struct {
	Path    string  `json:"path" required:"true"`
	Content *string `json:"content" required:"true"`
}

// Run executes the tool.
func (t *WriteFileTool) Run(ctx context.Context, args json.RawMessage) string {
	return withTimeout(ctx, "write_file", fileToolTimeout, func(ctx context.Context) string {
		return t.run(ctx, args)
	})
}

func (t *WriteFileTool) run(ctx context.Context, args json.RawMessage) string {
	var a writeFileArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	// Guaranteed non-nil by the required check in parseArgs; a missing `content`
	// key never reaches this point (it would truncate the file).
	content := *a.Content

	path := resolveToolPath(a.Path, t.workspace)
	resolvedPath := absOrOriginal(path)

	// Create parent directories
	dir := filepath.Dir(path)
	resolvedDir := absOrOriginal(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return toolError("write_file", fmt.Sprintf("failed to create parent directory: %s: %v", formatResolvedPath(dir, resolvedDir), err))
	}

	// Bail out if the timeout already fired to avoid writing after the caller
	// received a timeout error.
	if ctx.Err() != nil {
		return toolError("write_file", "operation cancelled before write")
	}

	// Write file (overwrite)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return toolError("write_file", fmt.Sprintf("failed to write file: %s: %v", formatResolvedPath(a.Path, resolvedPath), err))
	}

	return toolResult("write_file", map[string]any{
		"path":  resolvedPath,
		"bytes": len(content),
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
			Description: "Edit a file with one or more exact-text replacements in a single call. Relative paths are resolved from workspace root. Each old_text must match exactly (trailing whitespace, smart quotes, Unicode dashes/spaces, BOM, and CRLF/LF differences are tolerated) and must be unique in the file; include enough surrounding context to make it unique. All edits are matched against the original file (not applied one after another) and must not overlap.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the file to edit.",
					},
					"edits": map[string]any{
						"type":        "array",
						"description": "One or more replacements applied in a single call. Each old_text must be unique in the file and the edits must not overlap.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"old_text": map[string]any{
									"type":        "string",
									"description": "The exact text to find and replace. Must be unique in the file.",
								},
								"new_text": map[string]any{
									"type":        "string",
									"description": "The text to replace with (may be empty to delete the matched text).",
								},
							},
							"required": []string{"old_text", "new_text"},
						},
					},
				},
				"required": []string{"path", "edits"},
			},
		},
	}
}

// editEntry is one replacement within an edit_file call. It accepts snake_case
// (old_text/new_text), camelCase (oldText/newText), and *_string aliases so
// edits authored by different models all parse.
//
// The aliases are declared as struct tags rather than hand-rolled in a custom
// UnmarshalJSON, and that is deliberate: a type that decodes itself is opaque
// to reflection, so parseArgs' recursive normalizer cannot see inside it and
// every unknown key within an edit — notably `replace_all`, which models
// trained on other agent harnesses emit routinely — would be silently dropped.
// Declared this way, the normalizer rewrites the aliases AND rejects unknown
// keys with a path (`edits[0].replace_all`), so the model learns the flag is
// unsupported instead of retrying against a confusing uniqueness error.
type editEntry struct {
	OldText string `json:"old_text" alias:"oldText,old_string"`
	NewText string `json:"new_text" alias:"newText,new_string"`
}

// editFileArgs are the arguments for edit_file.
type editFileArgs struct {
	Path  string      `json:"path" required:"true"`
	Edits []editEntry `json:"edits" required:"true"`
}

// prepareEditArguments normalizes inbound arguments into the canonical
// {path, edits:[...]} shape before strict parsing, mirroring pi-mono's
// prepareArguments. It parses edits sent as a JSON string, and folds a legacy
// single-edit form (top-level old_text/new_text, including aliases) into edits,
// dropping those legacy keys so the strict parser does not reject them.
func prepareEditArguments(args json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil {
		return args // let parseArgs surface the malformed-JSON error
	}

	// Some models send edits as a JSON-encoded string instead of an array.
	if raw, ok := m["edits"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			var arr []json.RawMessage
			if json.Unmarshal([]byte(s), &arr) == nil {
				if b, err := json.Marshal(arr); err == nil {
					m["edits"] = b
				}
			}
		}
	}

	hasEdits := false
	if raw, ok := m["edits"]; ok {
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
			hasEdits = true
		}
	}

	// Fold a legacy single-edit form into edits[].
	if legacyOld := firstPresent(m, "old_text", "oldText", "old_string"); !hasEdits && legacyOld != nil {
		entry := map[string]json.RawMessage{"old_text": legacyOld}
		if legacyNew := firstPresent(m, "new_text", "newText", "new_string"); legacyNew != nil {
			entry["new_text"] = legacyNew
		} else {
			entry["new_text"] = json.RawMessage(`""`)
		}
		if eb, err := json.Marshal([]map[string]json.RawMessage{entry}); err == nil {
			m["edits"] = eb
		}
	}

	// Drop folded legacy keys so the strict parser accepts the payload.
	for _, k := range []string{"old_text", "oldText", "old_string", "new_text", "newText", "new_string"} {
		delete(m, k)
	}

	if b, err := json.Marshal(m); err == nil {
		return b
	}
	return args
}

func firstPresent(m map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

// Run executes the tool.
func (t *EditFileTool) Run(ctx context.Context, args json.RawMessage) string {
	return withTimeout(ctx, "edit_file", fileToolTimeout, func(ctx context.Context) string {
		return t.run(ctx, args)
	})
}

func (t *EditFileTool) run(ctx context.Context, args json.RawMessage) string {
	var a editFileArgs
	if errMsg := parseArgs(prepareEditArguments(args), &a); errMsg != "" {
		return errMsg
	}

	path := resolveToolPath(a.Path, t.workspace)
	resolvedPath := absOrOriginal(path)
	displayPath := formatResolvedPath(a.Path, resolvedPath)

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return toolError("edit_file", fmt.Sprintf("file not found: %s", displayPath))
		}
		return toolError("edit_file", fmt.Sprintf("failed to read file: %s: %v", displayPath, err))
	}

	// Strip a UTF-8 BOM and normalize line endings for matching; both are
	// restored on write so the file's original encoding is preserved.
	bom, body := stripBom(string(content))
	ending := detectLineEnding(body)
	normalized := normalizeToLF(body)

	edits := make([]editPair, len(a.Edits))
	for i, e := range a.Edits {
		edits[i] = editPair{oldText: e.OldText, newText: e.NewText}
	}

	newBody, usedFuzzy, err := applyEditsToNormalizedContent(normalized, edits, displayPath)
	if err != nil {
		return toolError("edit_file", err.Error())
	}

	final := bom + restoreLineEndings(newBody, ending)
	if ctx.Err() != nil {
		return toolError("edit_file", "operation cancelled before write")
	}
	if err := os.WriteFile(path, []byte(final), 0644); err != nil {
		return toolError("edit_file", fmt.Sprintf("failed to write file: %s: %v", displayPath, err))
	}

	return toolResult("edit_file", map[string]any{
		"path":         displayPath,
		"replacements": len(a.Edits),
		"fuzzy":        usedFuzzy,
	}, "")
}
