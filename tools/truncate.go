package tools

import "fmt"

func truncateWithNotice(content string, maxChars int) (string, bool) {
	if maxChars <= 0 || len(content) <= maxChars {
		return content, false
	}

	omitted := len(content) - maxChars
	marker := fmt.Sprintf("\n\n... [truncated %d characters] ...\n\n", omitted)

	// Budget the marker into the allowed length so total output <= maxChars + marker.
	half := maxChars / 2
	head := content[:half]
	tail := content[len(content)-(maxChars-half):]

	return head + marker + tail, true
}
