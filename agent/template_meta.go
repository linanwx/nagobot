package agent

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// TemplateMeta holds the YAML frontmatter fields of an agent template.
type TemplateMeta struct {
	Name             string   `yaml:"name"`
	Description      string   `yaml:"description"`
	Specialty        string   `yaml:"specialty"`
	Provider         string   `yaml:"provider"`
	Model            string   `yaml:"model"`                        // deprecated: use Specialty; kept for backward compatibility
	Sections         []string `yaml:"sections,omitempty"`           // per-session sections to auto-append (e.g. user_memory_section)
	ContextWindowCap string   `yaml:"context_window_cap,omitempty"` // human-readable cap (e.g. "64k", "200k", "1M") — clamps effective context window for this agent
}

// ParseTokenAmount parses a human-readable token count.
// Supports "64k", "1M", "200000", "200_000", "200,000". Case insensitive.
// Returns 0 for empty or unparseable strings.
func ParseTokenAmount(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, ",", "")

	multiplier := 1
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'k', 'K':
			multiplier = 1000
			s = s[:n-1]
		case 'm', 'M':
			multiplier = 1000000
			s = s[:n-1]
		}
	}

	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0
	}
	return n * multiplier
}

// ParseTemplate extracts YAML frontmatter and body from a template string.
func ParseTemplate(content string) (meta TemplateMeta, body string, hasHeader bool, err error) {
	header, body, hasHeader := splitFrontMatter(content)
	if !hasHeader {
		return meta, content, false, nil
	}

	if err := yaml.Unmarshal([]byte(header), &meta); err != nil {
		return meta, content, true, err
	}
	// Backward compatibility: fall back to deprecated "model" field.
	if meta.Specialty == "" && meta.Model != "" {
		meta.Specialty = meta.Model
	}
	return meta, body, true, nil
}


func splitFrontMatter(content string) (header string, body string, ok bool) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", content, false
	}

	rest := normalized[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", content, false
	}

	header = rest[:end]
	body = rest[end+len("\n---\n"):]
	return header, body, true
}
