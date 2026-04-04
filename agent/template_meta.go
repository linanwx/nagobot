package agent

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// TemplateMeta holds the YAML frontmatter fields of an agent template.
type TemplateMeta struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Specialty   string   `yaml:"specialty"`
	Provider    string   `yaml:"provider"`
	Model       string   `yaml:"model"`              // deprecated: use Specialty; kept for backward compatibility
	Sections    []string `yaml:"sections,omitempty"`  // per-session sections to auto-append (e.g. user_memory_section)
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
