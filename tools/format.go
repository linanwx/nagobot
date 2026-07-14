package tools

import (
	"time"

	"github.com/linanwx/nagobot/thread/msg"
)

// nowFn returns the current time. Indirection so tests can stub it.
var nowFn = time.Now

// nowStamp formats the current time as RFC3339 in local timezone.
// RFC3339 is self-describing — the offset is included.
func nowStamp() string {
	return nowFn().Format(time.RFC3339)
}

// toolResult builds a YAML frontmatter + body tool result via msg.* helpers
// so quoting, escaping, and multi-line value handling are correct by
// construction. Leading fields are "tool", "status: ok", "time: <RFC3339>";
// remaining fields are appended in sorted order. body is appended verbatim
// after a blank line; pass body="" for header-only output.
func toolResult(tool string, fields map[string]any, body string) string {
	mapping, err := msg.SortedFieldsMapping(
		[][2]string{{"tool", tool}, {"status", "ok"}, {"time", nowStamp()}},
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

// ErrorSeverity grades a tool failure for LOGGING ONLY. It never changes what
// the model sees: the result still carries `status: error` and an "Error: "
// body, is still fed back into the conversation, and is still counted by
// IsToolError. Nothing is suppressed — only the log level moves.
//
// It exists because a single `logger.Error` for every failure made the log
// useless: measured over four months, 1225 anti-bot 403s and 143 upstream 404s
// drowned the ~170 genuine argument bugs, all at identical severity. "Reddit
// blocked us again" is not an error condition of this program.
type ErrorSeverity string

const (
	// SeverityError is our own defect: bad arguments, malformed payloads, a
	// broken invariant. Someone has to change code. This is the default —
	// an unclassified failure is an error until proven otherwise.
	SeverityError ErrorSeverity = "error"
	// SeverityWarn is recoverable and usually model-driven: a page fetched
	// fine but could not be parsed, a pagination offset past the end. Worth
	// noticing in aggregate, not worth waking anyone up.
	SeverityWarn ErrorSeverity = "warn"
	// SeverityInfo is the outside world saying no: any 4xx/5xx from a
	// third-party host. Expected, unactionable, and constant.
	SeverityInfo ErrorSeverity = "info"
)

// toolError builds a YAML frontmatter error result at the default severity.
// Body starts with "Error: " for backward compatibility with legacy detection.
func toolError(tool, message string) string {
	return toolErrorSev(tool, SeverityError, message)
}

// toolErrorSev is toolError with an explicit severity. The severity rides in
// the frontmatter so the runner — which only ever sees the result string — can
// pick a log level without re-parsing the message text.
func toolErrorSev(tool string, sev ErrorSeverity, message string) string {
	fields := [][2]string{{"tool", tool}, {"status", "error"}, {"time", nowStamp()}}
	if sev != SeverityError {
		fields = append(fields, [2]string{"severity", string(sev)})
	}
	mapping, err := msg.SortedFieldsMapping(fields, nil)
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

// ToolErrorSeverity reports how a tool error should be logged. Anything without
// an explicit severity — including every legacy bare "Error: ..." string — is
// SeverityError, so a tool that has not opted in keeps its old log level.
func ToolErrorSeverity(result string) ErrorSeverity {
	switch {
	case msg.HasFrontmatterKeyValue(result, "severity", string(SeverityInfo)):
		return SeverityInfo
	case msg.HasFrontmatterKeyValue(result, "severity", string(SeverityWarn)):
		return SeverityWarn
	default:
		return SeverityError
	}
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
