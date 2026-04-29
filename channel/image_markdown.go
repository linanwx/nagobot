package channel

import (
	"regexp"
	"strings"
)

// parsedImage is one Markdown image reference extracted from text.
// RawPath is the path string as written (absolute or relative); resolution
// against the workspace happens at delivery time.
type parsedImage struct {
	Alt     string
	RawPath string
}

// imageSyntaxRe matches ![alt](path) and ![alt](path "title").
//   - alt: any chars except ']'
//   - path: any chars except whitespace and ')'
//   - optional title: whitespace + "..." before close paren
var imageSyntaxRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

// parseMarkdownImages returns image references that appear in normal text,
// skipping any that are inside fenced code blocks (``` or ~~~) or inline
// code spans (`...`). Order is preserved.
func parseMarkdownImages(text string) []parsedImage {
	var out []parsedImage
	lines := strings.Split(text, "\n")
	inFence := false
	var fenceMarker string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inFence {
			if strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
				fenceMarker = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			inFence = true
			fenceMarker = "```"
			continue
		}
		if strings.HasPrefix(trimmed, "~~~") {
			inFence = true
			fenceMarker = "~~~"
			continue
		}
		out = append(out, extractFromLine(line)...)
	}
	return out
}

// extractFromLine scans a single non-fenced line, skipping inline code spans.
func extractFromLine(line string) []parsedImage {
	var out []parsedImage
	i := 0
	for i < len(line) {
		if line[i] == '`' {
			end := strings.IndexByte(line[i+1:], '`')
			if end < 0 {
				return out
			}
			i = i + 1 + end + 1
			continue
		}
		if line[i] == '!' && i+1 < len(line) && line[i+1] == '[' {
			loc := imageSyntaxRe.FindStringSubmatchIndex(line[i:])
			if loc != nil && loc[0] == 0 {
				match := imageSyntaxRe.FindStringSubmatch(line[i : i+loc[1]])
				out = append(out, parsedImage{Alt: match[1], RawPath: match[2]})
				i += loc[1]
				continue
			}
		}
		i++
	}
	return out
}
