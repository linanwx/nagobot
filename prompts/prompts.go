// Package prompts holds the model-facing prose that the Go code emits: wake
// action hints, tool descriptions, tool-result guidance, the headers of
// injected prompt blocks, and the notices hooks inject mid-turn.
//
// Every piece is one markdown file under templates/, addressed by its path
// without the extension ("wake/progress" is templates/wake/progress.md). The
// files ship inside the binary, are synced to {workspace}/system/prompts by
// `onboard --sync`, and a running daemon reads them from there (UseDir), so a
// wording change on a deployment takes effect without a release. The
// canonical copy is still the one in this package: `onboard --sync` overwrites
// the workspace, exactly as it does for system sections.
//
// Code declares each prompt it uses (Declare) with the placeholders it fills.
// A file whose placeholders do not match its declaration is rejected: at
// daemon start that fails the start, and on a live reload the previous text
// is kept and the rejection is logged at Error. A declared prompt never falls
// back to text compiled into the code.
package prompts

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed all:templates
var embedded embed.FS

// Embedded returns the prompt files built into the binary, rooted at the
// directory that `onboard --sync` copies to {workspace}/system/prompts.
func Embedded() fs.FS {
	sub, err := fs.Sub(embedded, "templates")
	if err != nil {
		panic(err) // the embed directive guarantees the directory exists
	}
	return sub
}

// Vars maps placeholder names (without braces) to their values.
type Vars map[string]string

// Prompt is one declared prompt file.
type Prompt struct {
	key     string
	vars    []string
	oneLine bool
	tool    bool
}

// Option configures a declaration.
type Option func(*Prompt)

// WithVars names the {{PLACEHOLDER}}s the file must contain, no more and no
// fewer, and that Render must be given.
func WithVars(names ...string) Option {
	return func(p *Prompt) { p.vars = append(p.vars, names...) }
}

// OneLine collapses every run of whitespace in the file to one space, for
// text that lands in a one-line YAML value. The file itself may then be
// written one sentence per line.
func OneLine() Option {
	return func(p *Prompt) { p.oneLine = true }
}

var (
	declMu   sync.Mutex
	declared = map[string]*Prompt{}
)

// Declare registers a prompt file. It is meant for package-level vars, so
// every prompt is known before the daemon validates its files. Declaring the
// same key twice panics: two call sites sharing one file would silently
// constrain each other's placeholders.
func Declare(key string, opts ...Option) *Prompt {
	p := &Prompt{key: key}
	for _, o := range opts {
		o(p)
	}
	sort.Strings(p.vars)
	declMu.Lock()
	defer declMu.Unlock()
	if _, dup := declared[key]; dup {
		panic("prompts: duplicate declaration of " + key)
	}
	declared[key] = p
	return p
}

// Key is the prompt's file path without the .md extension.
func (p *Prompt) Key() string { return p.key }

// Declared returns every declared prompt key, sorted.
func Declared() []string {
	declMu.Lock()
	defer declMu.Unlock()
	keys := make([]string, 0, len(declared))
	for k := range declared {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func declaration(key string) *Prompt {
	declMu.Lock()
	defer declMu.Unlock()
	return declared[key]
}

// Text returns a prompt that takes no placeholders.
func (p *Prompt) Text() string {
	return p.Render(nil)
}

// Render returns the prompt with its placeholders filled. A missing or
// unexpected variable is a programming error in the caller; it is logged at
// Error and the placeholder is left visible rather than silently blanked.
func (p *Prompt) Render(vars Vars) string {
	return fill(p, active().text(p.key).body, vars)
}

// fill substitutes placeholders in ONE pass over the template, so a value that
// itself contains "{{SOMETHING}}" (a pinned message, a session key) is
// inserted literally and never expanded.
func fill(p *Prompt, text string, vars Vars) string {
	for _, name := range p.vars {
		if _, ok := vars[name]; !ok {
			reportError("prompt rendered without a declared variable", "key", p.key, "var", name)
		}
	}
	for name := range vars {
		if !containsString(p.vars, name) {
			reportError("prompt rendered with an undeclared variable", "key", p.key, "var", name)
		}
	}
	return placeholderRe.ReplaceAllStringFunc(text, func(m string) string {
		name := m[2 : len(m)-2]
		if v, ok := vars[name]; ok && containsString(p.vars, name) {
			return v
		}
		return m
	})
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// file is one parsed prompt file.
type file struct {
	body   string
	params map[string]string // tools only: schema property path -> description
}

var placeholderRe = regexp.MustCompile(`\{\{([A-Z0-9_]+)\}\}`)

// frontmatter is the optional YAML header of a prompt file. `note` is for the
// people editing the file and is never sent to a model.
type frontmatter struct {
	Note   string            `yaml:"note"`
	Params map[string]string `yaml:"params"`
}

// parse turns raw file content into a file, checking it against its
// declaration.
func parse(p *Prompt, raw string) (file, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	var fm frontmatter
	body := raw
	if strings.HasPrefix(raw, "---\n") {
		end := strings.Index(raw[4:], "\n---")
		if end < 0 {
			return file{}, fmt.Errorf("unterminated frontmatter")
		}
		if err := yaml.Unmarshal([]byte(raw[4:4+end]), &fm); err != nil {
			return file{}, fmt.Errorf("invalid frontmatter: %w", err)
		}
		body = raw[4+end+4:]
	}
	body = strings.TrimSpace(body)
	if p.oneLine {
		body = strings.Join(strings.Fields(body), " ")
	}
	if body == "" {
		return file{}, fmt.Errorf("empty body")
	}
	if !p.tool && len(fm.Params) > 0 {
		return file{}, fmt.Errorf("params frontmatter is only valid in tools/ files")
	}
	params := map[string]string{}
	for k, v := range fm.Params {
		params[k] = strings.TrimSpace(v)
	}

	found := map[string]bool{}
	for _, text := range append([]string{body}, mapValues(params)...) {
		for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
			found[m[1]] = true
		}
	}
	var missing, extra []string
	for _, v := range p.vars {
		if !found[v] {
			missing = append(missing, v)
		}
	}
	for v := range found {
		if !containsString(p.vars, v) {
			extra = append(extra, v)
		}
	}
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		return file{}, fmt.Errorf("placeholders do not match the declaration: missing %v, unknown %v", missing, extra)
	}
	return file{body: body, params: params}, nil
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// readFS reads every .md file in fsys keyed by its path without extension.
func readFS(fsys fs.FS) (map[string]string, error) {
	raw := map[string]string{}
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		raw[strings.TrimSuffix(path, ".md")] = string(data)
		return nil
	})
	return raw, err
}

// Check validates a prompt directory against every declaration: each declared
// prompt must have a well-formed file, and every file must be declared. It is
// what the daemon runs at start and what the drift test runs on the embedded
// copy.
func Check(fsys fs.FS) error {
	raw, err := readFS(fsys)
	if err != nil {
		return err
	}
	var errs []string
	for _, key := range Declared() {
		content, ok := raw[key]
		if !ok {
			errs = append(errs, key+".md: missing")
			continue
		}
		if _, err := parse(declaration(key), content); err != nil {
			errs = append(errs, key+".md: "+err.Error())
		}
	}
	for key := range raw {
		if declaration(key) == nil {
			errs = append(errs, key+".md: no code declares this prompt")
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("prompt files invalid:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// Note returns the `note` frontmatter of a prompt file's content: what the
// file is for, written for the people editing it. "" when there is none.
func Note(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return ""
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return ""
	}
	var fm frontmatter
	if yaml.Unmarshal([]byte(content[4:4+end]), &fm) != nil {
		return ""
	}
	return fm.Note
}
