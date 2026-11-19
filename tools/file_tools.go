package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/prompts"
	"github.com/linanwx/nagobot/provider"
	"os"
	"path/filepath"
	"strconv"
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
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type": "string",
			},
			"offset": map[string]any{
				"type": "integer",
			},
			"limit": map[string]any{
				"type": "integer",
			},
			"tail": map[string]any{
				"type": "integer",
			},
		},
		"required": []string{"path"},
	}
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "read_file",
			Description: promptToolReadFile.Describe(params, nil),
			Parameters:  params,
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
			return toolResult("read_file", fields, promptReadImageUnsupported.Text())
		}
		return toolResult("read_file", fields, promptReadImageDelegate.Render(prompts.Vars{"PATH": absPath}))
	}
	return toolResult("read_file", fields, fmt.Sprintf("<<media:%s:%s>>", mimeType, absPath))
}

// handleAudio returns audio data for audio-capable models or delegation guidance.
func (t *ReadFileTool) handleAudio(ctx context.Context, absPath, mimeType string, size int64) string {
	fields := map[string]any{"path": absPath, "type": mimeType, "size": size}
	rt := RuntimeContextFrom(ctx)
	if !rt.SupportsAudio {
		if !rt.AudioReaderConfigured {
			return toolResult("read_file", fields, promptReadAudioUnsupported.Text())
		}
		return toolResult("read_file", fields, promptReadAudioDelegate.Text())
	}
	return toolResult("read_file", fields, fmt.Sprintf("<<media:%s:%s>>", mimeType, absPath))
}

// handlePDF returns extraction guidance. PDFs are not read natively — the model
// must extract the document first (render pages to images, or extract text) and
// read the result, or use an MCP PDF reader.
func (t *ReadFileTool) handlePDF(ctx context.Context, absPath, mimeType string, size int64) string {
	fields := map[string]any{"path": absPath, "type": mimeType, "size": size}
	return toolResult("read_file", fields, promptReadPDF.Text())
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

	// A file ending in "\n" splits into a trailing empty element that is not a
	// line. Rendering it as one (the old behaviour) told the model the file had
	// one more newline at EOF than it does — and since a single-replacement edit
	// can only append by anchoring on the last line, that made every append
	// unmatchable through no fault of the model's. The file's own EOF convention
	// is invisible once every rendered line is terminated with "\n", so it is
	// reported explicitly instead.
	raw := string(content)
	endsWithNewline := strings.HasSuffix(raw, "\n")
	allLines := strings.Split(raw, "\n")
	if endsWithNewline {
		allLines = allLines[:len(allLines)-1]
	}
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
	// Stated only when the window actually reaches EOF: it is a fact about the
	// end of the file, and claiming it while showing the middle would be a claim
	// about bytes the model cannot see.
	if effEnd == totalLines {
		fields["ends_with_newline"] = endsWithNewline
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
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type": "string",
			},
			"content": map[string]any{
				"type": "string",
			},
		},
		"required": []string{"path", "content"},
	}
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "write_file",
			Description: promptToolWriteFile.Describe(params, nil),
			Parameters:  params,
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
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type": "string",
			},
			"old_text": map[string]any{
				"type": "string",
			},
			"new_text": map[string]any{
				"type": "string",
			},
		},
		"required": []string{"path", "old_text", "new_text"},
	}
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "edit_file",
			Description: promptToolEditFile.Describe(params, nil),
			Parameters:  params,
		},
	}
}

// editFileArgs are the arguments for edit_file: exactly one replacement.
//
// The tool briefly exposed an edits[] array (one call, N disjoint
// replacements). It was reverted: batching multiplied both failure modes.
// Malformed-JSON rejections went from 1 in the 107 days before to 87 in the 16
// days after — a nested array of long CJK strings with escaped quotes is simply
// harder to emit than a flat pair, and one stray full-width quote kills the
// whole call before parseArgs even runs. And because the engine matches every
// old_text against the ORIGINAL file (all-or-nothing, by design — see
// applyEditsToNormalizedContent), one stale old_text out of N discards the N-1
// that would have applied, so the per-call failure rate compounds as
// 1-(1-p)^n. Observed old_text mismatches roughly doubled per day too. One
// replacement per call keeps the model's payload flat and the blast radius at
// one edit.
//
// The engine still takes []editPair and keeps its multi-edit semantics; only
// this tool boundary is single. Do NOT reintroduce edits[] here without
// re-reading the numbers above.
//
// Aliases (oldText/old_string) are declared as struct tags rather than a
// hand-rolled UnmarshalJSON, and that is deliberate: a type that decodes itself
// is opaque to reflection, so parseArgs' recursive normalizer could not see
// inside it and unknown keys — notably `replace_all`, which models trained on
// other agent harnesses emit routinely — would be silently dropped. Declared
// this way, the normalizer rewrites the aliases AND rejects unknown keys by
// name, so the model learns the flag is unsupported instead of retrying against
// a confusing uniqueness error.
//
// NewText is a *string, not a string, for the reason write_file.Content is:
// `required:"true"` means present AND non-empty, but "" is the legitimate way
// to delete the matched text. As a plain string, a dropped new_text key would
// pass as "" and silently delete old_text instead of failing.
type editFileArgs struct {
	Path    string  `json:"path" required:"true"`
	OldText string  `json:"old_text" alias:"oldText,old_string" required:"true"`
	NewText *string `json:"new_text" alias:"newText,new_string" required:"true"`
}

// Run executes the tool.
func (t *EditFileTool) Run(ctx context.Context, args json.RawMessage) string {
	return withTimeout(ctx, "edit_file", fileToolTimeout, func(ctx context.Context) string {
		return t.run(ctx, args)
	})
}

func (t *EditFileTool) run(ctx context.Context, args json.RawMessage) string {
	var a editFileArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
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

	edits := []editPair{{oldText: a.OldText, newText: *a.NewText}}

	newBody, usedFuzzy, err := applyEditsToNormalizedContent(normalized, edits, displayPath)
	if err != nil {
		if errors.Is(err, ErrTextNotFound) {
			return toolError("edit_file", err.Error()+editRecoveryHint(resolvedPath, normalized))
		}
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
		"path":  displayPath,
		"fuzzy": usedFuzzy,
	}, "")
}

// editHintMaxLines bounds the file that a mismatch error inlines whole. Under
// it the model gets the bytes it failed to quote; over it the error costs more
// context than the retry is worth, so the model is pointed at grep instead.
const editHintMaxLines = 200

// editRecoveryHint is what turns a failed edit into a retry that can succeed.
//
// Every edit_file failure measured on the deployment over eight days was this
// one error, and the message alone gave the model nothing it did not already
// believe — so it re-guessed, and the second and third guesses failed the same
// way. Two thirds of the failures were an old_text that differed from the file
// by a dropped word, an added `**`, or a colon's width: a difference the model
// cannot see without the real bytes in front of it.
//
// Small files are therefore inlined whole, with no line numbers, because the
// point is to hand back something that can be copied verbatim. Large ones are
// not: a 200-line file of CJK prose is already a heavy error payload, and past
// that the cost outgrows the retry. Those get a recipe instead — grep to find
// the line, read_file to see that window, copy from there — which is the same
// two calls the model would otherwise spend guessing.
//
// content is the LF-normalized body the matcher actually ran against, not the
// raw file: quoting the bytes the model must reproduce for a match is the whole
// job, and on a CRLF file the raw bytes are not those bytes.
//
// It names the file only inside the recipe, and by resolvedPath: the error line
// it is appended to already carries the path, and the display form
// ("input (resolved: …)") is not something a model can paste into the next call.
func editRecoveryHint(resolvedPath, content string) string {
	lines := strings.Count(content, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		lines++
	}
	if lines <= editHintMaxLines && len(content) <= readFileMaxBytes {
		return "\n\n" + promptEditNotFoundContents.Render(prompts.Vars{"LINES": strconv.Itoa(lines)}) + "\n" + content
	}
	return "\n\n" + promptEditNotFoundRecipe.Render(prompts.Vars{"LINES": strconv.Itoa(lines), "PATH": strconv.Quote(resolvedPath)})
}
