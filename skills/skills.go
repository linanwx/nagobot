// Package skills provides the skill system for nagobot.
// Skills are reusable prompt templates that can be loaded dynamically.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a skill definition.
type Skill struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Prompt      string   `yaml:"prompt"`
	Tags        []string `yaml:"tags,omitempty"`
	Examples    []string `yaml:"examples,omitempty"`
	Enabled     bool     `yaml:"enabled"`
}

// Registry holds loaded skills.
type Registry struct {
	skills map[string]*Skill
}

// NewRegistry creates a new skill registry.
func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]*Skill),
	}
}

// Register adds a skill to the registry.
func (r *Registry) Register(s *Skill) {
	r.skills[s.Name] = s
}

// Get returns a skill by name.
func (r *Registry) Get(name string) (*Skill, bool) {
	s, ok := r.skills[name]
	return s, ok
}

// List returns all registered skills.
func (r *Registry) List() []*Skill {
	skills := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		skills = append(skills, s)
	}
	return skills
}

// EnabledSkills returns all enabled skills.
func (r *Registry) EnabledSkills() []*Skill {
	var enabled []*Skill
	for _, s := range r.skills {
		if s.Enabled {
			enabled = append(enabled, s)
		}
	}
	return enabled
}

// Names returns the names of all registered skills.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.skills))
	for name := range r.skills {
		names = append(names, name)
	}
	return names
}

// LoadFromDirectory loads all skills from a directory.
// Supports both .yaml/.yml files and .md files with YAML frontmatter.
func (r *Registry) LoadFromDirectory(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No skills directory is okay
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

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
			return fmt.Errorf("failed to load skill %s: %w", name, loadErr)
		}

		if skill != nil {
			r.Register(skill)
		}
	}

	return nil
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

	// Default to enabled if not specified
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
// enabled: true
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
			Name:    name,
			Prompt:  content,
			Enabled: true,
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

// BuildPromptSection builds a prompt section from enabled skills.
func (r *Registry) BuildPromptSection() string {
	enabled := r.EnabledSkills()
	if len(enabled) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Skills\n\n")
	sb.WriteString("You have the following specialized skills:\n\n")

	for _, s := range enabled {
		sb.WriteString(fmt.Sprintf("### %s\n", s.Name))
		if s.Description != "" {
			sb.WriteString(fmt.Sprintf("%s\n\n", s.Description))
		}
		sb.WriteString(s.Prompt)
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// ============================================================================
// Built-in Skills
// ============================================================================

// RegisterBuiltinSkills registers built-in skills.
func (r *Registry) RegisterBuiltinSkills() {
	// Code Review skill
	r.Register(&Skill{
		Name:        "code-review",
		Description: "Perform code reviews with best practices",
		Prompt: `When reviewing code:
1. Check for bugs, security issues, and performance problems
2. Suggest improvements for readability and maintainability
3. Ensure code follows project conventions
4. Point out missing error handling or edge cases
5. Be constructive and explain the reasoning behind suggestions`,
		Tags:    []string{"development", "review"},
		Enabled: true,
	})

	// Git Commit skill
	r.Register(&Skill{
		Name:        "git-commit",
		Description: "Generate meaningful git commit messages",
		Prompt: `When generating git commit messages:
1. Use conventional commit format: type(scope): description
2. Types: feat, fix, docs, style, refactor, test, chore
3. Keep the first line under 72 characters
4. Add a body for complex changes explaining why, not what
5. Reference related issues when applicable`,
		Tags:    []string{"git", "development"},
		Enabled: true,
	})

	// Explain Code skill
	r.Register(&Skill{
		Name:        "explain-code",
		Description: "Explain code in simple terms",
		Prompt: `When explaining code:
1. Start with a high-level overview of what the code does
2. Break down complex logic into digestible steps
3. Explain the purpose of key variables and functions
4. Point out any design patterns or idioms used
5. Mention potential gotchas or important considerations`,
		Tags:    []string{"learning", "development"},
		Enabled: true,
	})

	// Refactor skill
	r.Register(&Skill{
		Name:        "refactor",
		Description: "Suggest code refactoring improvements",
		Prompt: `When refactoring code:
1. Identify code smells and anti-patterns
2. Suggest incremental improvements, not rewrites
3. Maintain backward compatibility when possible
4. Improve naming for clarity
5. Extract repeated logic into reusable functions
6. Add or improve documentation as needed`,
		Tags:    []string{"development", "improvement"},
		Enabled: true,
	})
}
