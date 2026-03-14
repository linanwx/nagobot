package tools

import (
	"fmt"
	"sort"
	"strings"
)

// toolResult builds a YAML frontmatter + body tool result.
// The "tool" field is always first, followed by "status: ok", then remaining fields sorted.
func toolResult(tool string, fields map[string]any, body string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("tool: %s\n", tool))
	sb.WriteString("status: ok\n")

	// Sort remaining fields for deterministic output.
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(formatYAMLField(k, fields[k]))
	}

	sb.WriteString("---")
	if body != "" {
		sb.WriteString("\n\n")
		sb.WriteString(body)
	}
	return sb.String()
}

// toolError builds a YAML frontmatter error result.
// Body starts with "Error: " for backward compatibility with legacy detection.
func toolError(tool, message string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("tool: %s\n", tool))
	sb.WriteString("status: error\n")
	sb.WriteString("---\n\n")
	sb.WriteString("Error: ")
	sb.WriteString(message)
	return sb.String()
}

// IsToolError checks whether a tool result represents an error.
// Supports YAML format (status: error) and legacy format (Error: prefix).
func IsToolError(result string) bool {
	if strings.HasPrefix(result, "Error:") {
		return true
	}
	// Check YAML frontmatter for status: error.
	if strings.HasPrefix(result, "---\n") {
		if idx := strings.Index(result[4:], "\n---"); idx >= 0 {
			header := result[4 : 4+idx]
			for _, line := range strings.Split(header, "\n") {
				if strings.TrimSpace(line) == "status: error" {
					return true
				}
			}
		}
	}
	return false
}

// CmdResult builds a YAML frontmatter string for CLI command output.
// The "command" field is always first, followed by "status: ok", then remaining fields sorted.
func CmdResult(command string, fields map[string]any, body string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("command: %s\n", command))
	sb.WriteString("status: ok\n")

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(formatYAMLField(k, fields[k]))
	}

	sb.WriteString("---")
	if body != "" {
		sb.WriteString("\n\n")
		sb.WriteString(body)
	}
	return sb.String()
}

// CmdError builds a YAML frontmatter error string for CLI command output.
func CmdError(command, message string) string {
	return fmt.Sprintf("---\ncommand: %s\nstatus: error\n---\n\nError: %s", command, message)
}

// formatYAMLField formats a single key-value pair for YAML output.
func formatYAMLField(key string, value any) string {
	switch v := value.(type) {
	case string:
		if needsYAMLQuoting(v) {
			return fmt.Sprintf("%s: %q\n", key, v)
		}
		return fmt.Sprintf("%s: %s\n", key, v)
	case bool:
		return fmt.Sprintf("%s: %v\n", key, v)
	case int:
		return fmt.Sprintf("%s: %d\n", key, v)
	case int64:
		return fmt.Sprintf("%s: %d\n", key, v)
	default:
		return fmt.Sprintf("%s: %v\n", key, v)
	}
}

// needsYAMLQuoting returns true if the string value needs quoting in YAML.
func needsYAMLQuoting(s string) bool {
	if s == "" {
		return true
	}
	for _, c := range s {
		switch c {
		case ':', '#', '{', '}', '[', ']', ',', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
			return true
		}
	}
	// Quote if looks like a number or boolean.
	switch strings.ToLower(s) {
	case "true", "false", "yes", "no", "null", "~":
		return true
	}
	return false
}
