// Package skills provides the skill system for nagobot.
// Skills are reusable prompt templates that can be loaded dynamically.
package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Skill represents a skill definition.
//
// Description is the single source of truth for routing: it is what the prompt
// renders as {{SKILLS}} for the LLM, and what the pre-think skill retriever
// embeds. There is deliberately no parallel `tags` channel — one existed, was
// invisible to the LLM (BuildPromptSection emits only slug + description), and
// so let retrieval and routing drift apart. Anything worth routing on belongs in
// Description, where both readers can see it.
type Skill struct {
	Name        string   `yaml:"name"`
	Slug        string   `yaml:"-"` // Directory name, used as registry key and invocation name.
	Description string   `yaml:"description"`
	Prompt      string   `yaml:"prompt"`
	Examples    []string `yaml:"examples,omitempty"`
	Dir         string   `yaml:"-"` // Absolute path to skill directory (if directory-based).
}

// Registry holds loaded skills.
type Registry struct {
	skills       map[string]*Skill
	mu           sync.RWMutex
	lastSnapshot dirSnapshot // cached file modtimes for change detection
	dirs         []string    // directories used by last ReloadFromDirectories call
}

// NewRegistry creates a new skill registry.
func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]*Skill),
	}
}

// Register adds a skill to the registry, keyed by Slug.
func (r *Registry) Register(s *Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[s.Slug] = s
}

// Get returns a skill by name.
func (r *Registry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	return s, ok
}

// List returns all registered skills.
func (r *Registry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.skills))
	for name := range r.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	skills := make([]*Skill, 0, len(names))
	for _, name := range names {
		skills = append(skills, r.skills[name])
	}
	return skills
}

// LoadFromDirectory loads all skills from a directory.
// Supports both .yaml/.yml files and .md files with YAML frontmatter.
//
// A returned error means some skills failed to load, NOT that none did: the
// ones that parsed are registered regardless. Callers should log the error and
// keep going — that is the whole point of the isolation.
func (r *Registry) LoadFromDirectory(dir string) error {
	loaded, err := loadSkillsFromDirectory(dir)

	r.mu.Lock()
	defer r.mu.Unlock()
	for name, skill := range loaded {
		r.skills[name] = skill
	}

	return err
}

// ReloadFromDirectory replaces current skills with the latest files from dir.
// As with LoadFromDirectory, a non-nil error is a partial failure: the skills
// that did parse are installed.
func (r *Registry) ReloadFromDirectory(dir string) error {
	loaded, err := loadSkillsFromDirectory(dir)
	r.mu.Lock()
	r.skills = loaded
	r.mu.Unlock()
	return err
}

// LoadFromDirectories loads skills from multiple directories.
// Later directories override earlier ones (user skills override built-in).
// A bad skill in one directory does not stop the others from loading.
func (r *Registry) LoadFromDirectories(dirs ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for _, dir := range dirs {
		loaded, err := loadSkillsFromDirectory(dir)
		if err != nil {
			errs = append(errs, err)
		}
		for name, skill := range loaded {
			r.skills[name] = skill
		}
	}
	return errors.Join(errs...)
}

// ReloadFromDirectories replaces current skills with files from multiple directories.
// Later directories override earlier ones (user skills override built-in).
// Skips the full reload if no file modtimes have changed since the last call.
func (r *Registry) ReloadFromDirectories(dirs ...string) error {
	snap := takeDirSnapshot(dirs)
	r.mu.RLock()
	same := snap.equals(r.lastSnapshot)
	r.mu.RUnlock()
	if same {
		return nil
	}

	merged := make(map[string]*Skill)
	var errs []error
	for _, dir := range dirs {
		loaded, err := loadSkillsFromDirectory(dir)
		if err != nil {
			errs = append(errs, err)
		}
		for name, skill := range loaded {
			merged[name] = skill
		}
	}

	r.mu.Lock()
	r.skills = merged
	r.dirs = dirs
	// The snapshot is only recorded on a clean load. A broken skill therefore
	// re-parses and re-warns on every turn instead of warning once and then
	// going quiet — a skill that is missing because it does not parse must stay
	// loud until it is fixed, and this also picks the fix up the moment it lands.
	if len(errs) == 0 {
		r.lastSnapshot = snap
	} else {
		r.lastSnapshot = dirSnapshot{}
	}
	r.mu.Unlock()
	return errors.Join(errs...)
}

// Reload forces a full reload from the directories used by the last
// ReloadFromDirectories call, bypassing the snapshot cache. This is useful
// when new skills are installed mid-turn and need to be discovered immediately.
func (r *Registry) Reload() error {
	r.mu.RLock()
	dirs := r.dirs
	r.mu.RUnlock()
	if len(dirs) == 0 {
		return nil
	}
	// Clear the snapshot so ReloadFromDirectories bypasses its cache.
	r.mu.Lock()
	r.lastSnapshot = dirSnapshot{}
	r.mu.Unlock()
	return r.ReloadFromDirectories(dirs...)
}

// dirSnapshot records file modtimes for change detection.
type dirSnapshot struct {
	files map[string]int64 // path → modtime (UnixNano)
}

func takeDirSnapshot(dirs []string) dirSnapshot {
	snap := dirSnapshot{files: make(map[string]int64)}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			if e.IsDir() {
				// For directory-based skills, stat the SKILL.md inside.
				if sf := FindSkillFile(path); sf != "" {
					if info, err := os.Stat(sf); err == nil {
						snap.files[sf] = info.ModTime().UnixNano()
					}
				}
			} else {
				if info, err := e.Info(); err == nil {
					snap.files[path] = info.ModTime().UnixNano()
				}
			}
		}
	}
	return snap
}

func (s dirSnapshot) equals(other dirSnapshot) bool {
	if len(s.files) != len(other.files) {
		return false
	}
	for k, v := range s.files {
		if ov, ok := other.files[k]; !ok || v != ov {
			return false
		}
	}
	return true
}

// loadSkillsFromDirectory loads every skill it can and reports the ones it
// could not, rather than failing the whole directory at the first bad file.
//
// The isolation is the point. A skills directory is not curated — manage-skills
// installs from ClawHub, where thousands of community skills are one clone away
// — and an early return here meant a single malformed SKILL.md deleted every
// other skill in the directory: the callers only log the error and carry on, so
// the assistant would come up with an empty registry and no idea why. The trap
// is easy to spring, too: an unquoted YAML scalar cannot contain ": ", so a
// description as ordinary as "the WRITE path: add a key" fails to parse.
//
// A bad skill is skipped and named in the returned error, never swallowed. The
// caller logs it; the other skills keep working.
func loadSkillsFromDirectory(dir string) (map[string]*Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*Skill{}, nil // No skills directory is okay
		}
		return nil, err // Cannot read the directory at all — nothing to salvage.
	}

	loaded := make(map[string]*Skill)
	var errs []error
	for _, entry := range entries {
		// Directory-based skill: look for SKILL.md or SKILLS.md inside.
		if entry.IsDir() {
			slug := entry.Name()
			skillFile := FindSkillFile(filepath.Join(dir, slug))
			if skillFile == "" {
				continue
			}
			skill, loadErr := loadMarkdownSkill(skillFile, slug)
			if loadErr != nil {
				errs = append(errs, fmt.Errorf("skill %s (%s): %w", slug, skillFile, loadErr))
				continue
			}
			if skill != nil {
				skill.Slug = slug
				skill.Dir = filepath.Join(dir, slug)
				loaded[slug] = skill
			}
			continue
		}

		// Flat file skill (legacy compat).
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		slug := strings.TrimSuffix(name, ext)

		var skill *Skill
		var loadErr error

		switch ext {
		case ".yaml", ".yml":
			skill, loadErr = loadYAMLSkill(filepath.Join(dir, name))
		case ".md":
			skill, loadErr = loadMarkdownSkill(filepath.Join(dir, name), slug)
		default:
			continue
		}

		if loadErr != nil {
			errs = append(errs, fmt.Errorf("skill %s: %w", name, loadErr))
			continue
		}

		if skill != nil {
			if skill.Slug == "" {
				skill.Slug = slug
			}
			loaded[skill.Slug] = skill
		}
	}

	return loaded, errors.Join(errs...)
}

// FindSkillFile returns the path to SKILL.md or SKILLS.md in the given
// directory, preferring SKILL.md. Returns "" if neither exists.
func FindSkillFile(dir string) string {
	for _, name := range []string{"SKILL.md", "SKILLS.md"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// loadYAMLSkill loads a skill from a YAML file.
func loadYAMLSkill(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var skill Skill
	if err := yaml.Unmarshal(data, &skill); err != nil {
		return nil, err
	}

	slug := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	skill.Slug = slug
	if skill.Name == "" {
		skill.Name = slug
	}

	return &skill, nil
}

// loadMarkdownSkill loads a skill from a Markdown file with YAML frontmatter.
// slug is the directory or file stem name used as the invocation key.
func loadMarkdownSkill(path string, slug string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)

	// Check for frontmatter
	if !strings.HasPrefix(content, "---") {
		// No frontmatter, treat entire file as prompt
		return &Skill{
			Name:   slug,
			Slug:   slug,
			Prompt: content,
		}, nil
	}

	// Parse frontmatter
	parts := strings.SplitN(content[3:], "---", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid frontmatter format")
	}

	var skill Skill
	if err := yaml.Unmarshal([]byte(parts[0]), &skill); err != nil {
		return nil, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}

	// The rest is the prompt
	skill.Prompt = strings.TrimSpace(parts[1])
	skill.Slug = slug

	// Default name from slug
	if skill.Name == "" {
		skill.Name = slug
	}

	return &skill, nil
}

// BuildPromptSection builds a compact skill summary for the system prompt.
// Full skill prompts are loaded on demand via the use_skill tool.
func (r *Registry) BuildPromptSection() string {
	list := r.List()
	if len(list) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, s := range list {
		sb.WriteString(fmt.Sprintf("- **%s**", s.Slug))
		if s.Description != "" {
			sb.WriteString(fmt.Sprintf(": %s", s.Description))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// SkillNames returns the slugs of all registered skills in sorted order.
func (r *Registry) SkillNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.skills))
	for name := range r.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetSkillPrompt returns the full prompt and directory for a skill by slug.
func (r *Registry) GetSkillPrompt(name string) (prompt string, dir string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, found := r.skills[name]
	if !found {
		return "", "", false
	}
	return s.Prompt, s.Dir, true
}
