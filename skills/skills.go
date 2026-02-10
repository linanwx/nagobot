// Package skills provides the skill system for nagobot.
// Skills are reusable prompt templates that can be loaded dynamically.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Skill represents a skill definition.
type Skill struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Prompt      string   `yaml:"prompt"`
	Tags        []string `yaml:"tags,omitempty"`
	Examples    []string `yaml:"examples,omitempty"`
	Dir         string   `yaml:"-"` // Absolute path to skill directory (if directory-based).
}

// Registry holds loaded skills.
type Registry struct {
	skills map[string]*Skill
	mu     sync.RWMutex
}

// NewRegistry creates a new skill registry.
func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]*Skill),
	}
}

// Register adds a skill to the registry.
func (r *Registry) Register(s *Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[s.Name] = s
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
	skills := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		skills = append(skills, s)
	}
	return skills
}

// LoadFromDirectory loads all skills from a directory.
// Supports both .yaml/.yml files and .md files with YAML frontmatter.
func (r *Registry) LoadFromDirectory(dir string) error {
	loaded, err := loadSkillsFromDirectory(dir)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for name, skill := range loaded {
		r.skills[name] = skill
	}

	return nil
}

// ReloadFromDirectory replaces current skills with the latest files from dir.
func (r *Registry) ReloadFromDirectory(dir string) error {
	loaded, err := loadSkillsFromDirectory(dir)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.skills = loaded
	r.mu.Unlock()
	return nil
}

func loadSkillsFromDirectory(dir string) (map[string]*Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*Skill{}, nil // No skills directory is okay
		}
		return nil, err
	}

	loaded := make(map[string]*Skill)
	for _, entry := range entries {
		// Directory-based skill: look for SKILL.md inside.
		if entry.IsDir() {
			skillFile := filepath.Join(dir, entry.Name(), "SKILL.md")
			if _, statErr := os.Stat(skillFile); statErr != nil {
				continue
			}
			skill, loadErr := loadMarkdownSkill(skillFile)
			if loadErr != nil {
				return nil, fmt.Errorf("failed to load skill %s/SKILL.md: %w", entry.Name(), loadErr)
			}
			if skill != nil {
				if skill.Name == "" {
					skill.Name = entry.Name()
				}
				skill.Dir = filepath.Join(dir, entry.Name())
				loaded[skill.Name] = skill
			}
			continue
		}

		// Flat file skill (legacy compat).
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))

		var skill *Skill
		var loadErr error

		switch ext {
		case ".yaml", ".yml":
			skill, loadErr = loadYAMLSkill(filepath.Join(dir, name))
		case ".md":
			skill, loadErr = loadMarkdownSkill(filepath.Join(dir, name))
		default:
			continue
		}

		if loadErr != nil {
			return nil, fmt.Errorf("failed to load skill %s: %w", name, loadErr)
		}

		if skill != nil {
			loaded[skill.Name] = skill
		}
	}

	return loaded, nil
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

	if skill.Name == "" {
		// Use filename as name
		skill.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	return &skill, nil
}

// loadMarkdownSkill loads a skill from a Markdown file with YAML frontmatter.
// Format:
// ---
// name: skill-name
// description: Short description
// tags: [tag1, tag2]
// ---
// # Skill Prompt Content
// The rest of the markdown is the prompt.
func loadMarkdownSkill(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)

	// Check for frontmatter
	if !strings.HasPrefix(content, "---") {
		// No frontmatter, treat entire file as prompt
		name := strings.TrimSuffix(filepath.Base(path), ".md")
		return &Skill{
			Name:   name,
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

	// Default name from filename
	if skill.Name == "" {
		skill.Name = strings.TrimSuffix(filepath.Base(path), ".md")
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
	sb.WriteString("## Skills\n\n")
	sb.WriteString("Available skills (use the `use_skill` tool to load full instructions):\n\n")

	for _, s := range list {
		sb.WriteString(fmt.Sprintf("- **%s**", s.Name))
		if s.Description != "" {
			sb.WriteString(fmt.Sprintf(": %s", s.Description))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// SkillNames returns the names of all registered skills.
func (r *Registry) SkillNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.skills))
	for name := range r.skills {
		names = append(names, name)
	}
	return names
}

// GetSkillPrompt returns the full prompt for a skill by name.
func (r *Registry) GetSkillPrompt(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	if !ok {
		return "", false
	}
	return s.Prompt, true
}
