package agent

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// StringList is a YAML field that accepts either a scalar ("a") or a sequence
// ("[a, b]"), decoding both to a []string. This keeps agent templates lenient:
// a hand-written `specialty: pdf` parses the same as `specialty: [pdf]`.
type StringList []string

// UnmarshalYAML accepts a scalar or a sequence node.
func (s *StringList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		if strings.TrimSpace(value.Value) == "" {
			*s = nil
			return nil
		}
		*s = StringList{value.Value}
		return nil
	}
	var arr []string
	if err := value.Decode(&arr); err != nil {
		return err
	}
	*s = StringList(arr)
	return nil
}

// TemplateMeta holds the YAML frontmatter fields of an agent template.
type TemplateMeta struct {
	Name             string     `yaml:"name"`
	Description      string     `yaml:"description"`
	Specialties      StringList `yaml:"specialty"` // one or more specialty tags; scalar or array
	// SourceSpecialties maps a wake source (e.g. "heartbeat") to a specialty
	// list. When the turn's wake source matches a key, model resolution tries
	// that list (left-to-right) before the agent rule and the basic Specialties.
	SourceSpecialties map[string]StringList `yaml:"source_specialty,omitempty"`
	Provider          string                `yaml:"provider"`
	Sections         []string   `yaml:"sections,omitempty"`           // per-session sections to auto-append (e.g. user_memory_section)
	ContextWindowCap string     `yaml:"context_window_cap,omitempty"` // human-readable cap (e.g. "64k", "200k", "1M") — clamps effective context window for this agent
	TierLossyMode    string     `yaml:"tier_lossy_mode,omitempty"`    // lossy compression mode: "slide_window" (phase 1) | "ratio" (future)
	TierLossyKeep    int        `yaml:"tier_lossy_keep,omitempty"`    // slide_window: last N user-assistant turns to retain
	DisableTools     bool       `yaml:"disable_tools,omitempty"`      // when true, the agent runs with no tools (the tool list is not constructed)
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
	// Normalize specialties: trim each and drop empties.
	cleaned := meta.Specialties[:0]
	for _, sp := range meta.Specialties {
		if sp = strings.TrimSpace(sp); sp != "" {
			cleaned = append(cleaned, sp)
		}
	}
	meta.Specialties = cleaned
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
