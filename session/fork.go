package session

import (
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/provider"
)

const (
	forkArgsPreviewRunes  = 100
	forkResultPreviewRunes = 200
)

// ForkMessages produces a stripped version of a session's message history
// suitable for a fork session (e.g. scheduler). The output is deterministic:
// same input always produces the same output.
//
// Stripping rules:
//   - User messages: keep content; YAML frontmatter reduced to sender + time only.
//   - Assistant messages with content: keep content verbatim; reasoning stripped.
//   - Assistant tool calls + their tool results: folded into a single assistant
//     message with one-line summaries per tool call.
//   - HeartbeatTrim turns: entire turn (user + assistant + tool messages) skipped.
//   - Reasoning fields (ReasoningContent, ReasoningDetails): always cleared.
//   - Output contains only "user" and "assistant" role messages.
func ForkMessages(msgs []provider.Message) []provider.Message {
	turns := splitTurns(msgs)
	var out []provider.Message
	for _, turn := range turns {
		if isHeartbeatTrimTurn(turn) {
			continue
		}
		forkTurn := forkOneTurn(turn)
		out = append(out, forkTurn...)
	}
	return out
}

// splitTurns groups messages into turns. Each turn starts with a user message
// and includes all subsequent non-user messages until the next user message.
// Leading non-user messages (before any user message) form their own turn.
func splitTurns(msgs []provider.Message) [][]provider.Message {
	var turns [][]provider.Message
	var current []provider.Message
	for _, m := range msgs {
		if m.Role == "user" && len(current) > 0 {
			turns = append(turns, current)
			current = nil
		}
		current = append(current, m)
	}
	if len(current) > 0 {
		turns = append(turns, current)
	}
	return turns
}

// isHeartbeatTrimTurn returns true if any message in the turn has HeartbeatTrim set.
func isHeartbeatTrimTurn(turn []provider.Message) bool {
	for i := range turn {
		if turn[i].HeartbeatTrim {
			return true
		}
	}
	return false
}

// forkOneTurn processes a single turn (user msg + assistant/tool responses).
func forkOneTurn(turn []provider.Message) []provider.Message {
	var out []provider.Message

	for _, m := range turn {
		switch m.Role {
		case "user":
			out = append(out, forkUserMessage(m))

		case "assistant":
			forked := forkAssistantMessage(m, turn)
			if forked.Content != "" {
				out = append(out, forked)
			}

		// "tool" messages are folded into their parent assistant — skip here.
		}
	}
	return out
}

// forkUserMessage strips YAML frontmatter down to sender + time, keeping the body.
func forkUserMessage(m provider.Message) provider.Message {
	content := m.Content
	content = stripFrontmatter(content)

	return provider.Message{
		Role:      "user",
		Content:   content,
		Timestamp: m.Timestamp,
		Source:    m.Source,
	}
}

// stripFrontmatter reduces YAML frontmatter to only sender and time fields.
// If no frontmatter is present, returns content unchanged.
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}
	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx < 0 {
		return content
	}
	endIdx += 4
	yamlBlock := content[4:endIdx]
	body := content[endIdx+5:] // skip "\n---\n"

	// Extract sender and time from the YAML block.
	var sender, timeVal string
	for _, line := range strings.Split(yamlBlock, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "sender":
			sender = val
		case "time":
			timeVal = val
		}
	}

	if sender == "" && timeVal == "" {
		// No relevant fields found — return body only.
		return strings.TrimLeft(body, "\n")
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	if sender != "" {
		sb.WriteString("sender: ")
		sb.WriteString(sender)
		sb.WriteString("\n")
	}
	if timeVal != "" {
		sb.WriteString("time: ")
		sb.WriteString(timeVal)
		sb.WriteString("\n")
	}
	sb.WriteString("---\n")
	sb.WriteString(body)
	return sb.String()
}

// forkAssistantMessage converts an assistant message:
//   - If it has tool calls: produces tool call summaries (folding in tool results from the turn).
//   - Content is kept verbatim.
//   - Reasoning is always stripped.
//   - The tool summaries and content are combined into a single message.
func forkAssistantMessage(m provider.Message, turn []provider.Message) provider.Message {
	var parts []string

	// Fold tool calls into one-line summaries.
	if len(m.ToolCalls) > 0 {
		for _, tc := range m.ToolCalls {
			result := findToolResult(turn, tc.ID)
			summary := formatToolSummary(tc, result)
			parts = append(parts, summary)
		}
	}

	// Keep assistant content verbatim.
	content := strings.TrimSpace(m.Content)
	if content != "" {
		parts = append(parts, content)
	}

	return provider.Message{
		Role:      "assistant",
		Content:   strings.Join(parts, "\n"),
		Timestamp: m.Timestamp,
	}
}

// findToolResult finds the tool result message matching a tool call ID within the turn.
func findToolResult(turn []provider.Message, toolCallID string) string {
	for _, m := range turn {
		if m.Role == "tool" && m.ToolCallID == toolCallID {
			return m.Content
		}
	}
	return ""
}

// formatToolSummary formats a single tool call + result as a one-line summary.
func formatToolSummary(tc provider.ToolCall, result string) string {
	name := tc.Function.Name
	args := runePreview(tc.Function.Arguments, forkArgsPreviewRunes)
	res := runePreview(result, forkResultPreviewRunes)

	if res == "" {
		return fmt.Sprintf("[tool: %s(%s)]", name, args)
	}
	return fmt.Sprintf("[tool: %s(%s) → %s]", name, args, res)
}

// runePreview returns the first n runes of s, appending "..." if truncated.
// Newlines are collapsed to spaces for single-line display.
func runePreview(s string, n int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
