package channel

import (
	"regexp"
	"strings"
)

type parsedImage struct {
	Alt     string
	RawPath string
}

var imageSyntaxRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

// parseMarkdownImages returns image references in normal text, skipping any
// inside fenced code blocks (``` / ~~~) or inline code spans (`...`).
func parseMarkdownImages(text string) []parsedImage {
	if !strings.ContainsRune(text, '!') {
		return nil
	}
	var out []parsedImage
	inFence := false
	var fenceMarker string
	for _, line := range strings.Split(text, "\n") {
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

func extractFromLine(line string) []parsedImage {
	var out []parsedImage
	i := 0
	for i < len(line) {
		if line[i] == '`' {
			end := strings.IndexByte(line[i+1:], '`')
			if end < 0 {
				return out
			}
			i += 2 + end
			continue
		}
		if line[i] == '!' && i+1 < len(line) && line[i+1] == '[' {
			loc := imageSyntaxRe.FindStringSubmatchIndex(line[i:])
			if loc != nil && loc[0] == 0 {
				alt := line[i+loc[2] : i+loc[3]]
				path := line[i+loc[4] : i+loc[5]]
				out = append(out, parsedImage{Alt: alt, RawPath: path})
				i += loc[1]
				continue
			}
		}
		i++
	}
	return out
}

// stripMarkdownImages removes Markdown image references from text, sharing
// fence/inline-code awareness with parseMarkdownImages. Lines that consisted
// solely of an image reference become empty; runs of 3+ consecutive newlines
// are collapsed to 2 to avoid blank-line buildup. Channels whose markdown
// renderer would show a broken-image placeholder for unreachable URLs (e.g.
// WeCom AI Bot) call this before sending the text bubble — the actual image
// is delivered separately via ImageSender.
func stripMarkdownImages(text string) string {
	if !strings.ContainsRune(text, '!') {
		return text
	}
	inFence := false
	var fenceMarker string
	lines := strings.Split(text, "\n")
	for i, line := range lines {
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
		lines[i] = stripImagesFromLine(line)
	}
	out := strings.Join(lines, "\n")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}

func stripImagesFromLine(line string) string {
	var b strings.Builder
	i := 0
	for i < len(line) {
		if line[i] == '`' {
			end := strings.IndexByte(line[i+1:], '`')
			if end < 0 {
				b.WriteString(line[i:])
				return b.String()
			}
			b.WriteString(line[i : i+2+end])
			i += 2 + end
			continue
		}
		if line[i] == '!' && i+1 < len(line) && line[i+1] == '[' {
			loc := imageSyntaxRe.FindStringSubmatchIndex(line[i:])
			if loc != nil && loc[0] == 0 {
				i += loc[1]
				continue
			}
		}
		b.WriteByte(line[i])
		i++
	}
	return strings.TrimRight(b.String(), " \t")
}
