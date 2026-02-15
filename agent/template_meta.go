package agent

import (
	"strings"

	"gopkg.in/yaml.v3"
)

type templateMeta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Model       string `yaml:"model"`
}

func parseTemplate(content string) (meta templateMeta, body string, hasHeader bool, err error) {
	header, body, hasHeader := splitFrontMatter(content)
	if !hasHeader {
		return meta, content, false, nil
	}

	if err := yaml.Unmarshal([]byte(header), &meta); err != nil {
		return meta, content, true, err
	}
	return meta, body, true, nil
}

func stripFrontMatter(content string) string {
	_, body, hasHeader, err := parseTemplate(content)
	if err != nil || !hasHeader {
		return content
	}
	return strings.TrimLeft(body, "\n")
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
