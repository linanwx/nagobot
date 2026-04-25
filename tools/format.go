package tools

import (
	"github.com/linanwx/nagobot/thread/msg"
)

// toolResult builds a YAML frontmatter + body tool result via msg.* helpers
// so quoting, escaping, and multi-line value handling are correct by
// construction. The "tool" field is always first, then "status: ok", then
// remaining fields in sorted order. body is appended verbatim after a blank
// line; pass body="" for header-only output.
func toolResult(tool string, fields map[string]any, body string) string {
	mapping, err := msg.SortedFieldsMapping(
		[][2]string{{"tool", tool}, {"status", "ok"}},
		fields,
	)
	if err != nil {
		return ""
	}
	bodyText := ""
	if body != "" {
		bodyText = "\n" + body
	}
	return msg.BuildFrontmatter(mapping, bodyText)
}

// toolError builds a YAML frontmatter error result.
// Body starts with "Error: " for backward compatibility with legacy detection.
func toolError(tool, message string) string {
	mapping, err := msg.SortedFieldsMapping(
		[][2]string{{"tool", tool}, {"status", "error"}},
		nil,
	)
	if err != nil {
		return ""
	}
	return msg.BuildFrontmatter(mapping, "\nError: "+message)
}

// IsToolError checks whether a tool result represents an error.
// Supports YAML format (status: error) and legacy format (Error: prefix).
func IsToolError(result string) bool {
	if len(result) >= 6 && result[:6] == "Error:" {
		return true
	}
	return msg.HasFrontmatterKeyValue(result, "status", "error")
}

// CmdResult builds a YAML frontmatter string for CLI command output.
// The "command" field is always first, then "status: ok", then remaining
// fields in sorted order.
func CmdResult(command string, fields map[string]any, body string) string {
	mapping, err := msg.SortedFieldsMapping(
		[][2]string{{"command", command}, {"status", "ok"}},
		fields,
	)
	if err != nil {
		return ""
	}
	bodyText := ""
	if body != "" {
		bodyText = "\n" + body
	}
	return msg.BuildFrontmatter(mapping, bodyText)
}

// CmdError builds a YAML frontmatter error string for CLI command output.
func CmdError(command, message string) string {
	mapping, err := msg.SortedFieldsMapping(
		[][2]string{{"command", command}, {"status", "error"}},
		nil,
	)
	if err != nil {
		return ""
	}
	return msg.BuildFrontmatter(mapping, "\nError: "+message)
}

// CmdOutput builds a CLI command output with explicitly ordered key/value
// pairs. Use this for CLI commands that need a specific field order
// (e.g. command/status/action/...) or a non-"ok" status. No sorting is
// applied — the caller controls the order.
//
// All values are emitted via yaml.Marshal so quoting and escaping are
// handled correctly. Body is appended verbatim after a blank line; pass
// body="" for header-only output.
func CmdOutput(pairs [][2]string, body string) string {
	mapping := msg.NewMapping()
	for _, kv := range pairs {
		msg.AppendScalarPair(mapping, kv[0], kv[1])
	}
	bodyText := ""
	if body != "" {
		bodyText = "\n" + body
	}
	return msg.BuildFrontmatter(mapping, bodyText)
}
