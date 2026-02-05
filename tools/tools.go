// Package tools provides the tool interface and built-in tools.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pinkplumcom/nagobot/logger"
	"github.com/pinkplumcom/nagobot/provider"
)

// Tool is the interface for agent tools.
type Tool interface {
	// Def returns the tool definition for the LLM.
	Def() provider.ToolDef
	// Run executes the tool with the given arguments and returns the result.
	// Errors are returned as strings (for the LLM to interpret).
	Run(ctx context.Context, args json.RawMessage) string
}

// Registry holds registered tools.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools[t.Def().Function.Name] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Defs returns all tool definitions.
func (r *Registry) Defs() []provider.ToolDef {
	defs := make([]provider.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Def())
	}
	return defs
}

// Run executes a tool by name.
func (r *Registry) Run(ctx context.Context, name string, args json.RawMessage) string {
	t, ok := r.tools[name]
	if !ok {
		logger.Error("tool not found", "tool", name)
		return fmt.Sprintf("Error: unknown tool '%s'", name)
	}
	return t.Run(ctx, args)
}

// Names returns the names of all registered tools.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RegisterDefaultTools registers the default file tools.
func (r *Registry) RegisterDefaultTools(workspace string) {
	r.Register(&ReadFileTool{workspace: workspace})
	r.Register(&WriteFileTool{workspace: workspace})
	r.Register(&EditFileTool{workspace: workspace})
	r.Register(&ListDirTool{workspace: workspace})
	r.Register(&ExecTool{workspace: workspace})
	r.Register(&WebSearchTool{})
	r.Register(&WebFetchTool{})
}

// expandPath expands ~ to home directory and resolves the path.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[1:])
		}
	}
	return path
}

// ============================================================================
// ReadFileTool
// ============================================================================

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
			Description: "Read the contents of a file at the given path.",
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
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	path := expandPath(a.Path)

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("Error: file not found: %s", a.Path)
		}
		return fmt.Sprintf("Error: %v", err)
	}

	if info.IsDir() {
		return fmt.Sprintf("Error: %s is a directory, not a file", a.Path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Error: failed to read file: %v", err)
	}

	return string(content)
}

// ============================================================================
// WriteFileTool
// ============================================================================

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
			Description: "Write content to a file at the given path. Creates parent directories if needed.",
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
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	path := expandPath(a.Path)

	// Create parent directories
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Sprintf("Error: failed to create directory: %v", err)
	}

	// Write file
	if err := os.WriteFile(path, []byte(a.Content), 0644); err != nil {
		return fmt.Sprintf("Error: failed to write file: %v", err)
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(a.Content), a.Path)
}

// ============================================================================
// EditFileTool
// ============================================================================

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
			Description: "Edit a file by replacing specific text. The old_text must match exactly (including whitespace).",
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
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	path := expandPath(a.Path)

	// Read current content
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("Error: file not found: %s", a.Path)
		}
		return fmt.Sprintf("Error: failed to read file: %v", err)
	}

	contentStr := string(content)

	// Check if old_text exists
	count := strings.Count(contentStr, a.OldText)
	if count == 0 {
		return fmt.Sprintf("Error: text not found in file: %q", a.OldText)
	}
	if count > 1 {
		return fmt.Sprintf("Error: text appears %d times in file, must be unique. Provide more context.", count)
	}

	// Replace
	newContent := strings.Replace(contentStr, a.OldText, a.NewText, 1)

	// Write back
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return fmt.Sprintf("Error: failed to write file: %v", err)
	}

	return fmt.Sprintf("Successfully edited %s", a.Path)
}

// ============================================================================
// ListDirTool
// ============================================================================

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
			Description: "List the contents of a directory.",
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
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	path := expandPath(a.Path)

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("Error: directory not found: %s", a.Path)
		}
		return fmt.Sprintf("Error: %v", err)
	}

	if !info.IsDir() {
		return fmt.Sprintf("Error: %s is a file, not a directory", a.Path)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Sprintf("Error: failed to read directory: %v", err)
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

// ============================================================================
// ExecTool
// ============================================================================

// ExecTool executes shell commands.
type ExecTool struct {
	workspace string
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
						"description": "Optional working directory. Defaults to workspace.",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Optional timeout in seconds. Defaults to 60.",
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
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	// Default timeout
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 60
	}

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// Create command
	cmd := exec.CommandContext(execCtx, "sh", "-c", a.Command)

	// Set working directory
	if a.Workdir != "" {
		cmd.Dir = expandPath(a.Workdir)
	} else if t.workspace != "" {
		cmd.Dir = t.workspace
	}

	// Capture output
	output, err := cmd.CombinedOutput()

	if execCtx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("Error: command timed out after %d seconds\nPartial output:\n%s", timeout, string(output))
	}

	if err != nil {
		// Include output even on error (often contains useful info)
		return fmt.Sprintf("Command failed: %v\nOutput:\n%s", err, string(output))
	}

	result := string(output)
	if result == "" {
		return "(no output)"
	}

	// Truncate very long output
	const maxLen = 50000
	if len(result) > maxLen {
		result = result[:maxLen] + "\n... (output truncated)"
	}

	return result
}

// ============================================================================
// WebSearchTool
// ============================================================================

// WebSearchTool searches the web using DuckDuckGo.
type WebSearchTool struct{}

// Def returns the tool definition.
func (t *WebSearchTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "web_search",
			Description: "Search the web using DuckDuckGo and return results. Use for finding current information, documentation, etc.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The search query.",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Maximum number of results to return. Defaults to 5.",
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

// webSearchArgs are the arguments for web_search.
type webSearchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

// Run executes the tool.
func (t *WebSearchTool) Run(ctx context.Context, args json.RawMessage) string {
	var a webSearchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	if a.MaxResults <= 0 {
		a.MaxResults = 5
	}

	// Use DuckDuckGo HTML search (no API key required)
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(a.Query))

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return fmt.Sprintf("Error: failed to create request: %v", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error: search request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("Error: failed to read response: %v", err)
	}

	// Parse results (simple extraction from DuckDuckGo HTML)
	results := parseDuckDuckGoResults(string(body), a.MaxResults)

	if len(results) == 0 {
		return "No search results found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for: %s\n\n", a.Query))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet))
	}

	return sb.String()
}

// searchResult represents a single search result.
type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

// parseDuckDuckGoResults extracts results from DuckDuckGo HTML.
func parseDuckDuckGoResults(html string, maxResults int) []searchResult {
	var results []searchResult

	// Find result blocks - DuckDuckGo uses class="result__a" for links
	// and class="result__snippet" for snippets
	parts := strings.Split(html, `class="result__a"`)

	for i := 1; i < len(parts) && len(results) < maxResults; i++ {
		part := parts[i]

		// Extract URL
		urlStart := strings.Index(part, `href="`)
		if urlStart == -1 {
			continue
		}
		urlStart += 6
		urlEnd := strings.Index(part[urlStart:], `"`)
		if urlEnd == -1 {
			continue
		}
		rawURL := part[urlStart : urlStart+urlEnd]

		// DuckDuckGo wraps URLs, extract actual URL
		if strings.Contains(rawURL, "uddg=") {
			if decoded, err := url.QueryUnescape(rawURL); err == nil {
				if idx := strings.Index(decoded, "uddg="); idx != -1 {
					rawURL = decoded[idx+5:]
					if ampIdx := strings.Index(rawURL, "&"); ampIdx != -1 {
						rawURL = rawURL[:ampIdx]
					}
				}
			}
		}

		// Extract title
		titleEnd := strings.Index(part, "</a>")
		if titleEnd == -1 {
			continue
		}
		titlePart := part[:titleEnd]
		// Remove HTML tags from title
		title := stripHTMLTags(titlePart[urlEnd+1:])
		title = strings.TrimSpace(title)

		// Extract snippet
		snippet := ""
		if snippetStart := strings.Index(part, `class="result__snippet"`); snippetStart != -1 {
			snippetPart := part[snippetStart:]
			if gtIdx := strings.Index(snippetPart, ">"); gtIdx != -1 {
				snippetPart = snippetPart[gtIdx+1:]
				if endIdx := strings.Index(snippetPart, "</"); endIdx != -1 {
					snippet = stripHTMLTags(snippetPart[:endIdx])
					snippet = strings.TrimSpace(snippet)
				}
			}
		}

		if title != "" && rawURL != "" {
			results = append(results, searchResult{
				Title:   title,
				URL:     rawURL,
				Snippet: snippet,
			})
		}
	}

	return results
}

// stripHTMLTags removes HTML tags from a string.
func stripHTMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
		} else if r == '>' {
			inTag = false
		} else if !inTag {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// ============================================================================
// WebFetchTool
// ============================================================================

// WebFetchTool fetches content from a URL.
type WebFetchTool struct{}

// Def returns the tool definition.
func (t *WebFetchTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "web_fetch",
			Description: "Fetch the content of a web page. Returns the text content (HTML tags stripped for readability).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The URL to fetch.",
					},
					"raw": map[string]any{
						"type":        "boolean",
						"description": "If true, return raw HTML instead of stripped text. Defaults to false.",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

// webFetchArgs are the arguments for web_fetch.
type webFetchArgs struct {
	URL string `json:"url"`
	Raw bool   `json:"raw,omitempty"`
}

// Run executes the tool.
func (t *WebFetchTool) Run(ctx context.Context, args json.RawMessage) string {
	var a webFetchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	// Validate URL
	parsedURL, err := url.Parse(a.URL)
	if err != nil {
		return fmt.Sprintf("Error: invalid URL: %v", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "Error: only http and https URLs are supported"
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	if err != nil {
		return fmt.Sprintf("Error: failed to create request: %v", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error: request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Error: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	// Limit read size
	const maxSize = 500000
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return fmt.Sprintf("Error: failed to read response: %v", err)
	}

	content := string(body)

	if !a.Raw {
		// Strip HTML and clean up
		content = extractTextContent(content)
	}

	// Truncate if still too long
	const maxLen = 100000
	if len(content) > maxLen {
		content = content[:maxLen] + "\n... (content truncated)"
	}

	return content
}

// extractTextContent extracts readable text from HTML.
func extractTextContent(html string) string {
	// Remove script and style blocks
	for _, tag := range []string{"script", "style", "noscript"} {
		for {
			startTag := strings.Index(strings.ToLower(html), "<"+tag)
			if startTag == -1 {
				break
			}
			endTag := strings.Index(strings.ToLower(html[startTag:]), "</"+tag+">")
			if endTag == -1 {
				break
			}
			html = html[:startTag] + html[startTag+endTag+len("</"+tag+">"):]
		}
	}

	// Strip remaining HTML tags
	text := stripHTMLTags(html)

	// Clean up whitespace
	lines := strings.Split(text, "\n")
	var cleanLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}

	return strings.Join(cleanLines, "\n")
}
