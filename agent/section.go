package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/linanwx/nagobot/logger"
	"gopkg.in/yaml.v3"
)

// Per-session section key constants. Used in agent frontmatter and thread/run.go Set() calls.
const (
	SectionUserMemory      = "user_memory_section"
	SectionHeartbeatPrompt = "heartbeat_prompt_section"
)

// headingLevel returns the ATX heading level (1-6) of a markdown line, or 0 if not a heading.
func headingLevel(line string) int {
	trimmed := strings.TrimLeft(line, " ")
	if len(trimmed) == 0 || trimmed[0] != '#' {
		return 0
	}
	level := 0
	for _, ch := range trimmed {
		if ch == '#' {
			level++
		} else {
			break
		}
	}
	if level > 6 {
		return 0
	}
	rest := trimmed[level:]
	if rest != "" && rest[0] != ' ' {
		return 0
	}
	return level
}

// isCodeFenceLine detects ``` or ~~~ fence delimiters.
func isCodeFenceLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// normalizeHeadings shifts all markdown headings so the minimum level becomes targetLevel.
// Headings inside code fences are not shifted. Levels are capped at 6.
func normalizeHeadings(content string, targetLevel int) string {
	if content == "" {
		return content
	}
	lines := strings.Split(content, "\n")

	// Pass 1: find minimum heading level (outside code fences).
	minLevel := 7
	inFence := false
	for _, line := range lines {
		if isCodeFenceLine(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if lvl := headingLevel(line); lvl > 0 && lvl < minLevel {
			minLevel = lvl
		}
	}
	if minLevel > 6 {
		return content // no headings found
	}

	offset := targetLevel - minLevel
	if offset == 0 {
		return content
	}

	// Pass 2: shift headings.
	var sb strings.Builder
	sb.Grow(len(content) + abs(offset)*20)
	inFence = false
	for i, line := range lines {
		if isCodeFenceLine(line) {
			inFence = !inFence
		}
		if !inFence {
			if lvl := headingLevel(line); lvl > 0 {
				newLevel := lvl + offset
				if newLevel < 1 {
					newLevel = 1
				}
				if newLevel > 6 {
					newLevel = 6
				}
				trimmed := strings.TrimLeft(line, " ")
				rest := trimmed[lvl:] // " Title..." or ""
				sb.WriteString(strings.Repeat("#", newLevel))
				sb.WriteString(rest)
				if i < len(lines)-1 {
					sb.WriteString("\n")
				}
				continue
			}
		}
		sb.WriteString(line)
		if i < len(lines)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// --- Section Registry ---

// Section represents a system prompt section discovered from a .md file.
type Section struct {
	Name     string `yaml:"name"`
	Priority int    `yaml:"priority"`
	Parent   string `yaml:"parent,omitempty"`
	Body     string `yaml:"-"`
}

// SectionRegistry discovers, caches, and assembles sections from a directory.
type SectionRegistry struct {
	dir            string
	sections       map[string]*Section
	snapshot       dirSnapshot // reuse from agent/registry.go (same package)
	assembleCache  string     // cached Assemble() result
	assembleDirty  bool       // true when sections changed, cleared after Assemble()
	mu             sync.RWMutex
}

// NewSectionRegistry creates a registry for the given sections directory.
func NewSectionRegistry(dir string) *SectionRegistry {
	return &SectionRegistry{
		dir:      dir,
		sections: make(map[string]*Section),
	}
}

// Load scans the directory and parses all section files.
func (r *SectionRegistry) Load() error {
	return r.Reload()
}

// Reload re-scans the directory if any files changed (dirSnapshot guard).
func (r *SectionRegistry) Reload() error {
	snap := takeDirSnapshot([]string{r.dir})
	r.mu.RLock()
	same := snap.equals(r.snapshot)
	r.mu.RUnlock()
	if same {
		return nil
	}

	sections, err := loadSections(r.dir)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.sections = sections
	r.snapshot = snap
	r.assembleDirty = true
	r.mu.Unlock()
	return nil
}

// Count returns the number of loaded sections.
func (r *SectionRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sections)
}

// Assemble builds the complete prompt content from all sections.
// Root sections start at H1; children are one level deeper than their parent.
// The result is cached and only recomputed when sections change.
func (r *SectionRegistry) Assemble() string {
	r.mu.RLock()
	if !r.assembleDirty && r.assembleCache != "" {
		cached := r.assembleCache
		r.mu.RUnlock()
		return cached
	}
	sections := make([]*Section, 0, len(r.sections))
	for _, s := range r.sections {
		sections = append(sections, s)
	}
	r.mu.RUnlock()

	// Sort by (priority, name) for deterministic output.
	sort.Slice(sections, func(i, j int) bool {
		if sections[i].Priority != sections[j].Priority {
			return sections[i].Priority < sections[j].Priority
		}
		return sections[i].Name < sections[j].Name
	})

	// Build parent→children index and identify roots.
	children := make(map[string][]*Section)
	var roots []*Section
	knownNames := make(map[string]bool)
	for _, s := range sections {
		knownNames[s.Name] = true
	}
	for _, s := range sections {
		if s.Parent == "" || !knownNames[s.Parent] {
			if s.Parent != "" {
				logger.Warn("section has dangling parent, treating as root", "section", s.Name, "parent", s.Parent)
			}
			roots = append(roots, s)
		} else {
			children[s.Parent] = append(children[s.Parent], s)
		}
	}

	var sb strings.Builder
	for i, root := range roots {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		renderSection(&sb, root, children, 1)
	}
	result := strings.TrimSpace(sb.String())

	r.mu.Lock()
	r.assembleCache = result
	r.assembleDirty = false
	r.mu.Unlock()

	return result
}

// renderSection writes a section and its children recursively.
func renderSection(sb *strings.Builder, sec *Section, children map[string][]*Section, headingBase int) {
	content := strings.TrimSpace(sec.Body)
	if content != "" {
		sb.WriteString(normalizeHeadings(content, headingBase))
	}
	for _, child := range children[sec.Name] {
		sb.WriteString("\n\n")
		renderSection(sb, child, children, headingBase+1)
	}
}

func loadSections(dir string) (map[string]*Section, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*Section{}, nil
		}
		return nil, fmt.Errorf("read sections dir: %w", err)
	}

	result := make(map[string]*Section)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			logger.Warn("failed to read section file", "path", path, "err", err)
			continue
		}

		header, body, hasHeader := splitFrontMatter(string(data))
		sec := &Section{Body: strings.TrimSpace(body)}
		if hasHeader {
			if err := yaml.Unmarshal([]byte(header), sec); err != nil {
				logger.Warn("invalid section frontmatter", "path", path, "err", err)
				continue
			}
		}
		if sec.Name == "" {
			sec.Name = strings.TrimSuffix(e.Name(), ".md")
		}
		result[sec.Name] = sec
	}
	return result, nil
}
