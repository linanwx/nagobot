package tools

import "fmt"

func truncateWithNotice(content string, maxChars int) (string, bool) {
	runes := []rune(content)
	if maxChars <= 0 || len(runes) <= maxChars {
		return content, false
	}

	omitted := len(runes) - maxChars
	marker := fmt.Sprintf("\n\n... [truncated %d characters] ...\n\n", omitted)

	// Budget the marker into the allowed length so total output <= maxChars + marker.
	half := maxChars / 2
	head := string(runes[:half])
	tail := string(runes[len(runes)-(maxChars-half):])

	return head + marker + tail, true
}
