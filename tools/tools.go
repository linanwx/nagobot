// Package tools provides the tool interface and built-in tools.
package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"gopkg.in/yaml.v3"
)

// Tool timeout defaults. Grouped here for visibility.
const (
	fileToolTimeout   = 10 * time.Second
	globToolTimeout   = 30 * time.Second
	grepToolTimeout   = 30 * time.Second
	threadToolTimeout = 5 * time.Second
	wakeToolTimeout   = 5 * time.Second
	healthToolTimeout = 15 * time.Second
	skillToolTimeout  = 10 * time.Second
)

// withTimeout runs fn in a goroutine with a deadline. If the operation
// completes in time the result is returned; otherwise a timeout error is
// returned and the goroutine is left to finish in the background.
// This is the only safe way to bound blocking syscalls (os.ReadFile, etc.)
// that do not respect context cancellation.
func withTimeout(ctx context.Context, tool string, timeout time.Duration, fn func(ctx context.Context) string) string {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ch := make(chan string, 1)
	go func() {
		ch <- fn(ctx)
	}()

	select {
	case result := <-ch:
		return result
	case <-ctx.Done():
		// fn may have completed at the same instant — drain before returning an error.
		select {
		case result := <-ch:
			return result
		default:
		}
		if ctx.Err() == context.DeadlineExceeded {
			return toolError(tool, fmt.Sprintf("operation timed out after %v", timeout))
		}
		return toolError(tool, "operation cancelled")
	}
}

const (
	toolResultMaxRunes = 100000
	toolLogMaxRunes    = 50000
)

// Tool is the interface for agent tools.
type Tool interface {
	// Def returns the tool definition for the LLM.
	Def() provider.ToolDef
	// Run executes the tool with the given arguments and returns the result.
	// Errors are returned as strings (for the LLM to interpret).
	Run(ctx context.Context, args json.RawMessage) string
}

// Tool argument contract
//
// Go's encoding/json cannot distinguish an omitted key from an explicit `""`
// or `null` when decoding into a non-pointer field, and many models cannot
// omit a declared property at all — they emit every key and leave unused ones
// empty. Rather than fight that, every tool obeys one rule:
//
//	EMPTY STRING IS "NOT PROVIDED".
//
// Consequences, which are binding on every tool in this package:
//
//   - A parameter description must NEVER forbid the empty string ("do not pass
//     an empty string", "omit entirely"). Such an instruction is unsatisfiable
//     for a model that cannot omit fields, and pushes it toward sentinel values
//     like "default" or "none" that are then interpreted as real values. State
//     the empty-string semantics instead, the way web_search does:
//     "Search source. Empty to see guide."
//
//   - `required:"true"` means PRESENT AND NON-EMPTY. It cannot express
//     "required, but empty is a legal value" — for that, declare the field as a
//     POINTER (*string). The required check below tests pointers with IsNil, so
//     a *string field tagged required rejects a missing/null key while still
//     accepting "". write_file's `content` needs exactly this: content is
//     mandatory, yet "" is the legitimate way to create an empty file.
//
//   - Numeric parameters treat <= 0 as "use the default". A future numeric
//     parameter for which 0 is a meaningful value MUST be a pointer, or an
//     omitted key will silently mean 0.
//
//   - Identifier-ish fields (keys, ids, names) are trimmed before presence
//     checks; content fields (body, content, old_text, command) are never
//     trimmed, since leading/trailing whitespace is part of the payload.
//
// parseArgs decodes a tool's JSON arguments into target with three guards,
// applied RECURSIVELY through nested objects and arrays:
//
//  1. Alias compat: any field tagged `alias:"foo,bar"` also accepts foo/bar as
//     input keys (canonical key wins if both are present).
//  2. Unknown-key rejection: keys that match neither a declared field nor a
//     declared alias fail fast, with a JSON path (e.g. `sends[0].delay`), so a
//     misplaced key can never be silently dropped. This guard recurses: a
//     top-level-only check would let `dispatch(sends=[{delay:"1h"}])` and
//     `edit_file(edits=[{replace_all:true}])` through, and both fail silently
//     in ways the model reads as success.
//  3. Required-non-empty: fields tagged `required:"true"` must not be empty
//     (empty string / empty slice / empty map / nil pointer triggers an error).
//     Checked at the top level only; nested requirements belong to each tool's
//     semantic validation, which can produce a far better error message.
//
// These checks run centrally so every tool gets them without duplicated code.
func parseArgs[T any](args json.RawMessage, target *T) string {
	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" || trimmed == "null" {
		args = json.RawMessage("{}")
	}

	tv := reflect.TypeOf(target).Elem()
	if tv.Kind() != reflect.Struct {
		// Non-struct target: fallback to plain unmarshal. None of the built-in
		// tools hit this path today, but keep it safe.
		if err := json.Unmarshal(args, target); err != nil {
			return fmt.Sprintf("Error: invalid arguments: %v", err)
		}
		return ""
	}

	normalized, errMsg := normalizeArgs(args, tv, "")
	if errMsg != "" {
		return errMsg
	}
	if err := json.Unmarshal(normalized, target); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	vv := reflect.ValueOf(target).Elem()
	var missing []string
	for i := 0; i < tv.NumField(); i++ {
		f := tv.Field(i)
		if f.Tag.Get("required") != "true" {
			continue
		}
		fv := vv.Field(i)
		empty := false
		switch fv.Kind() {
		case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
			empty = fv.Len() == 0
		case reflect.Ptr, reflect.Interface:
			// Pointer fields are the escape hatch for "required, but empty is a
			// legal value": only a missing/null key is rejected here.
			empty = fv.IsNil()
		}
		if empty {
			missing = append(missing, fieldJSONName(f))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Sprintf("Error: missing or empty required argument(s): %s",
			strings.Join(missing, ", "))
	}
	return ""
}

var jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// fieldJSONName returns the JSON key a struct field decodes from.
func fieldJSONName(f reflect.StructField) string {
	jsonTag := f.Tag.Get("json")
	name := strings.SplitN(jsonTag, ",", 2)[0]
	if name == "" {
		name = f.Name
	}
	return name
}

// joinPath appends a key to a JSON path prefix ("" → "k", "a" → "a.k").
func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// normalizeArgs recursively rewrites aliases and rejects unknown keys against
// the shape of typ, returning the rewritten JSON. path is the JSON path prefix
// used in error messages ("" at the top level).
//
// Decode failures are NOT reported here: they are passed through untouched so
// that the caller's typed json.Unmarshal produces the canonical Go error
// ("cannot unmarshal string into Go struct field grepArgs.max_results of type
// int"), which names the field and the expected type. Re-reporting them from
// here would only make that message worse.
func normalizeArgs(raw json.RawMessage, typ reflect.Type, path string) (json.RawMessage, string) {
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	// Types that decode themselves are opaque to reflection — pass through.
	if typ == reflect.TypeOf(json.RawMessage(nil)) || reflect.PointerTo(typ).Implements(jsonUnmarshalerType) {
		return raw, ""
	}
	switch typ.Kind() {
	case reflect.Struct:
		return normalizeStructArgs(raw, typ, path)
	case reflect.Slice, reflect.Array:
		return normalizeSliceArgs(raw, typ, path)
	default:
		return raw, ""
	}
}

func normalizeStructArgs(raw json.RawMessage, typ reflect.Type, path string) (json.RawMessage, string) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return raw, "" // not an object (or JSON null) — let the typed decode speak
	}

	fields := make(map[string]reflect.StructField, typ.NumField())
	aliases := make(map[string]string)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Tag.Get("json") == "-" {
			continue
		}
		name := fieldJSONName(f)
		fields[name] = f
		for _, al := range strings.Split(f.Tag.Get("alias"), ",") {
			if al = strings.TrimSpace(al); al != "" {
				aliases[al] = name
			}
		}
	}

	// Apply aliases: alias → canonical. Canonical wins on conflict.
	for alias, canonical := range aliases {
		v, ok := m[alias]
		if !ok {
			continue
		}
		if _, exists := m[canonical]; !exists {
			m[canonical] = v
		}
		delete(m, alias)
	}

	// Reject unknown keys after alias rewrite.
	var unknown []string
	for k := range m {
		if _, ok := fields[k]; !ok {
			unknown = append(unknown, joinPath(path, k))
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		allowedList := make([]string, 0, len(fields))
		for k := range fields {
			allowedList = append(allowedList, k)
		}
		sort.Strings(allowedList)
		return nil, fmt.Sprintf("Error: unknown argument(s): %s (allowed: %s)",
			strings.Join(unknown, ", "), strings.Join(allowedList, ", "))
	}

	// Recurse into declared fields.
	for k, v := range m {
		nested, errMsg := normalizeArgs(v, fields[k].Type, joinPath(path, k))
		if errMsg != "" {
			return nil, errMsg
		}
		m[k] = nested
	}

	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Sprintf("Error: failed to normalize arguments: %v", err)
	}
	return out, ""
}

func normalizeSliceArgs(raw json.RawMessage, typ reflect.Type, path string) (json.RawMessage, string) {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil || arr == nil {
		return raw, "" // not an array (or JSON null) — let the typed decode speak
	}
	elem := typ.Elem()
	for i, v := range arr {
		nested, errMsg := normalizeArgs(v, elem, fmt.Sprintf("%s[%d]", path, i))
		if errMsg != "" {
			return nil, errMsg
		}
		arr[i] = nested
	}
	out, err := json.Marshal(arr)
	if err != nil {
		return nil, fmt.Sprintf("Error: failed to normalize arguments: %v", err)
	}
	return out, ""
}

// Registry holds registered tools.
type Registry struct {
	tools   map[string]Tool
	logsDir string
}

// DefaultToolsConfig provides defaults for built-in tools.
type DefaultToolsConfig struct {
	ExecTimeout         int
	WebSearchMaxResults int
	SearchProviders     map[string]SearchProvider
	SearchHealthChecker *SearchHealthChecker
	FetchProviders      map[string]FetchProvider
	FetchHealthChecker  *SearchHealthChecker // reused type — tracks fetch outcomes
	RestrictToWorkspace bool
	Skills              SkillProvider
	LogsDir             string // Log files directory for health diagnostics
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// SetLogsDir sets the directory for tool call log files.
func (r *Registry) SetLogsDir(dir string) {
	r.logsDir = strings.TrimSpace(dir)
}

// Clone returns a shallow copy of the registry.
func (r *Registry) Clone() *Registry {
	cloned := NewRegistry()
	cloned.logsDir = r.logsDir
	for name, tool := range r.tools {
		cloned.tools[name] = tool
	}
	return cloned
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

// Defs returns all tool definitions in deterministic (sorted) order.
// Sorted order is required for prompt caching — the cache prefix
// includes tools, and non-deterministic ordering causes cache misses.
func (r *Registry) Defs() []provider.ToolDef {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	defs := make([]provider.ToolDef, 0, len(names))
	for _, name := range names {
		defs = append(defs, r.tools[name].Def())
	}
	return defs
}

// Run executes a tool by name.
func (r *Registry) Run(ctx context.Context, name string, args json.RawMessage) string {
	start := time.Now()
	logger.Debug("tool call", "tool", name, "args", string(args))

	t, ok := r.tools[name]
	if !ok {
		logger.Error("tool not found", "tool", name)
		logger.Debug("tool call finished", "tool", name, "ok", false, "latencyMs", time.Since(start).Milliseconds())
		return fmt.Sprintf("Error: unknown tool '%s'", name)
	}

	result := t.Run(ctx, args)
	latency := time.Since(start)
	originalChars := len(result)
	result, truncated := truncateWithNotice(result, toolResultMaxRunes)
	if truncated {
		logger.Warn("tool output truncated",
			"tool", name,
			"originalChars", originalChars,
			"resultChars", len(result),
			"limit", toolResultMaxRunes,
		)
	}
	okResult := !IsToolError(result)
	logger.Debug(
		"tool call finished",
		"tool", name,
		"ok", okResult,
		"truncated", truncated,
		"resultChars", len(result),
		"originalChars", originalChars,
		"latencyMs", latency.Milliseconds(),
	)

	if r.logsDir != "" {
		go r.writeToolLog(name, args, result, start, latency, okResult)
	}

	return result
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
func (r *Registry) RegisterDefaultTools(workspace string, cfg DefaultToolsConfig) {
	r.Register(&ReadFileTool{workspace: workspace})
	r.Register(&WriteFileTool{workspace: workspace})
	r.Register(&GrepTool{workspace: workspace})
	r.Register(&GlobTool{workspace: workspace})
	r.Register(&EditFileTool{workspace: workspace})
	r.Register(NewExecTool(workspace, cfg.ExecTimeout, cfg.RestrictToWorkspace))
	r.Register(&HealthTool{Workspace: workspace, LogsDir: cfg.LogsDir})
	r.Register(&WebSearchTool{defaultMaxResults: cfg.WebSearchMaxResults, providers: cfg.SearchProviders, healthChecker: cfg.SearchHealthChecker})
	r.Register(&WebFetchTool{providers: cfg.FetchProviders, healthChecker: cfg.FetchHealthChecker})
	if cfg.Skills != nil {
		r.Register(NewUseSkillTool(cfg.Skills))
	}
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

// resolveToolPath resolves relative file tool paths from workspace.
func resolveToolPath(path, workspace string) string {
	path = expandPath(path)
	if path == "" || filepath.IsAbs(path) || workspace == "" {
		return path
	}
	return filepath.Join(workspace, path)
}

func (r *Registry) writeToolLog(name string, args json.RawMessage, result string, start time.Time, latency time.Duration, ok bool) {
	if err := os.MkdirAll(r.logsDir, 0755); err != nil {
		logger.Warn("failed to create tool logs dir", "dir", r.logsDir, "err", err)
		return
	}

	suffix := randomHex(3)
	fileName := fmt.Sprintf("%s-%s-%s.md", start.Format("2006-01-02-15-04-05"), name, suffix)

	status := "ok"
	if !ok {
		status = "error"
	}

	logResult := result
	if runes := []rune(logResult); len(runes) > toolLogMaxRunes {
		logResult = string(runes[:toolLogMaxRunes]) + "\n\n...(truncated)"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", name))
	sb.WriteString(fmt.Sprintf("- **Time**: %s\n", start.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("- **Latency**: %dms\n", latency.Milliseconds()))
	sb.WriteString(fmt.Sprintf("- **Status**: %s\n", status))
	sb.WriteString("\n## Request\n\n")
	sb.WriteString(formatArgsReadable(args))
	sb.WriteString("\n## Response\n\n")
	sb.WriteString(logResult)
	sb.WriteByte('\n')

	if err := os.WriteFile(filepath.Join(r.logsDir, fileName), []byte(sb.String()), 0644); err != nil {
		logger.Warn("failed to write tool log", "file", fileName, "err", err)
	}
}

func formatArgsReadable(args json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil || len(m) == 0 {
		return "(none)\n"
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return string(args) + "\n"
	}
	return "```yaml\n" + string(data) + "```\n"
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	}
	return hex.EncodeToString(buf)
}
