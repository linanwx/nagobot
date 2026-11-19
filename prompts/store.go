package prompts

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// reloadInterval bounds how often a directory-backed store stats its files.
// Prompts are read many times per LLM call (every tool description), so the
// check is throttled rather than run on each read.
const reloadInterval = 2 * time.Second

// store serves prompt files from one source: the embedded copy until UseDir
// is called, the workspace directory after.
type store struct {
	mu        sync.RWMutex
	dir       string // "" = embedded
	files     map[string]file
	snapshot  string
	lastCheck time.Time
}

var (
	activeMu    sync.Mutex
	activeStore *store
)

// active returns the store reads go through, loading the embedded copy on
// first use. Tests and non-daemon commands never call UseDir and read the
// embedded files, which are exactly what `onboard --sync` writes.
func active() *store {
	activeMu.Lock()
	defer activeMu.Unlock()
	if activeStore == nil {
		s := &store{files: map[string]file{}}
		raw, err := readFS(Embedded())
		if err != nil {
			reportError("reading embedded prompts failed", "err", err)
		}
		s.files = parseAll(raw, nil, "embedded")
		activeStore = s
	}
	return activeStore
}

// UseDir switches every later read to the prompt files in dir and validates
// them first. It returns an error, and switches nothing, when any declared
// prompt is missing or malformed: a daemon must not start on a prompt set it
// cannot serve.
func UseDir(dir string) error {
	if err := Check(os.DirFS(dir)); err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	raw, err := readFS(os.DirFS(dir))
	if err != nil {
		return err
	}
	s := &store{
		dir:       dir,
		files:     parseAll(raw, nil, dir),
		snapshot:  dirSnapshot(dir),
		lastCheck: time.Now(),
	}
	activeMu.Lock()
	activeStore = s
	activeMu.Unlock()
	return nil
}

// Dir reports the directory reads are served from, "" for the embedded copy.
func Dir() string {
	return active().dir
}

// parseAll parses every declared prompt in raw. A file that is missing or
// fails to parse keeps its previous version from prev, so a bad live edit
// degrades to the last good text instead of to nothing.
func parseAll(raw map[string]string, prev map[string]file, source string) map[string]file {
	out := map[string]file{}
	for _, key := range Declared() {
		p := declaration(key)
		content, ok := raw[key]
		if !ok {
			if old, had := prev[key]; had {
				out[key] = old
			}
			reportError("prompt file missing; keeping the previous text", "key", key, "source", source)
			continue
		}
		f, err := parse(p, content)
		if err != nil {
			if old, had := prev[key]; had {
				out[key] = old
			}
			reportError("prompt file rejected; keeping the previous text", "key", key, "source", source, "err", err)
			continue
		}
		out[key] = f
	}
	return out
}

func (s *store) text(key string) file {
	s.maybeReload()
	s.mu.RLock()
	f, ok := s.files[key]
	s.mu.RUnlock()
	if !ok {
		reportError("prompt has no loaded text", "key", key)
		return file{body: "[missing prompt: " + key + "]"}
	}
	return f
}

func (s *store) maybeReload() {
	if s.dir == "" {
		return
	}
	s.mu.Lock()
	if time.Since(s.lastCheck) < reloadInterval {
		s.mu.Unlock()
		return
	}
	s.lastCheck = time.Now()
	snap := dirSnapshot(s.dir)
	if snap == s.snapshot {
		s.mu.Unlock()
		return
	}
	s.snapshot = snap
	prev := s.files
	s.mu.Unlock()

	raw, err := readFS(os.DirFS(s.dir))
	if err != nil {
		reportError("reloading prompt files failed; keeping the previous set", "dir", s.dir, "err", err)
		return
	}
	files := parseAll(raw, prev, s.dir)
	s.mu.Lock()
	s.files = files
	s.mu.Unlock()
	logger.Info("prompt files reloaded", "dir", s.dir, "count", len(files))
}

// dirSnapshot fingerprints a directory by every file's path, size and mtime.
func dirSnapshot(dir string) string {
	var parts []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		parts = append(parts, fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

// reportError is a seam so tests can observe errors that are otherwise only
// logged.
var reportError = func(msg string, kv ...any) {
	logger.Error(msg, kv...)
}
