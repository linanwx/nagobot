package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/linanwx/nagobot/provider"
)

// ReadImageTool reads an image file and returns its contents for visual analysis.
type ReadImageTool struct {
	workspace string
}

// Def returns the tool definition.
func (t *ReadImageTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name: "read_image",
			Description: "Read an image file and return its contents for visual analysis. " +
				"Returns the image data if the current model supports vision, " +
				"or guidance to delegate to a vision-capable agent otherwise.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the image file to read.",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

type readImageArgs struct {
	Path string `json:"path"`
}

// imageExtensions maps common image file extensions to MIME types.
var imageExtensions = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".svg":  "image/svg+xml",
}

// detectImageMime returns the MIME type for an image file, or empty string if not an image.
// It tries extension first, then falls back to magic bytes detection.
func detectImageMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if m, ok := imageExtensions[ext]; ok {
		return m
	}
	// Fallback to Go's mime package.
	if m := mime.TypeByExtension(ext); strings.HasPrefix(m, "image/") {
		return m
	}
	// Fallback to magic bytes detection for unknown extensions (.dat, etc.).
	return detectImageMimeByMagic(path)
}

// detectImageMimeByMagic reads the first bytes of a file to identify the image format.
func detectImageMimeByMagic(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	header := make([]byte, 12)
	n, err := f.Read(header)
	if err != nil || n < 4 {
		return ""
	}
	header = header[:n]

	// JPEG: FF D8 FF
	if n >= 3 && header[0] == 0xFF && header[1] == 0xD8 && header[2] == 0xFF {
		return "image/jpeg"
	}
	// PNG: 89 50 4E 47
	if n >= 4 && header[0] == 0x89 && header[1] == 0x50 && header[2] == 0x4E && header[3] == 0x47 {
		return "image/png"
	}
	// GIF: 47 49 46 38
	if n >= 4 && header[0] == 0x47 && header[1] == 0x49 && header[2] == 0x46 && header[3] == 0x38 {
		return "image/gif"
	}
	// WebP: RIFF....WEBP
	if n >= 12 && header[0] == 0x52 && header[1] == 0x49 && header[2] == 0x46 && header[3] == 0x46 &&
		header[8] == 0x57 && header[9] == 0x45 && header[10] == 0x42 && header[11] == 0x50 {
		return "image/webp"
	}
	// BMP: 42 4D
	if n >= 2 && header[0] == 0x42 && header[1] == 0x4D {
		return "image/bmp"
	}

	return ""
}

// Run executes the tool.
func (t *ReadImageTool) Run(ctx context.Context, args json.RawMessage) string {
	var a readImageArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	path := resolveToolPath(a.Path, t.workspace)
	absPath := absOrOriginal(path)

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("Error: file not found: %s", formatResolvedPath(a.Path, absPath))
		}
		return fmt.Sprintf("Error: failed to stat file: %s: %v", formatResolvedPath(a.Path, absPath), err)
	}
	if info.IsDir() {
		return fmt.Sprintf("Error: path is a directory, not a file: %s", formatResolvedPath(a.Path, absPath))
	}

	mimeType := detectImageMime(path)
	if mimeType == "" {
		return fmt.Sprintf("Error: not a recognized image file: %s", formatResolvedPath(a.Path, absPath))
	}

	rt := RuntimeContextFrom(ctx)
	if !rt.SupportsVision {
		return "This is an image file. You cannot view images directly. " +
			"Use the spawn_thread tool to delegate to the 'imagereader' agent, " +
			"passing the original user message as the task."
	}

	return fmt.Sprintf("Image loaded (%s, %d bytes)\n<<media:%s:%s>>",
		mimeType, info.Size(), mimeType, absPath)
}
