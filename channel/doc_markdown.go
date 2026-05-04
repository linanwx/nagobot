package channel

import (
	"regexp"
	"strings"
)

type parsedDoc struct {
	Label   string
	RawPath string
}

var docSyntaxRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

// parseMarkdownDocs returns Markdown link references in normal text whose
// targets look like local file paths (not URLs, not anchors). Image syntax
// (![alt](path)) is recognised and skipped so the doc parser does not
// double-claim it. Fenced code blocks (``` / ~~~) and inline code spans (`...`)
// are excluded just like in parseMarkdownImages.
func parseMarkdownDocs(text string) []parsedDoc {
	if !strings.ContainsRune(text, '[') {
		return nil
	}
	var out []parsedDoc
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
		out = append(out, extractDocsFromLine(line)...)
	}
	return out
}

func extractDocsFromLine(line string) []parsedDoc {
	var out []parsedDoc
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
		// Skip image syntax `![alt](path)` so we don't claim its inner [alt](path).
		if line[i] == '!' && i+1 < len(line) && line[i+1] == '[' {
			loc := imageSyntaxRe.FindStringSubmatchIndex(line[i:])
			if loc != nil && loc[0] == 0 {
				i += loc[1]
				continue
			}
			i++
			continue
		}
		if line[i] == '[' {
			loc := docSyntaxRe.FindStringSubmatchIndex(line[i:])
			if loc != nil && loc[0] == 0 {
				label := line[i+loc[2] : i+loc[3]]
				path := line[i+loc[4] : i+loc[5]]
				if isLocalDocPath(path) {
					out = append(out, parsedDoc{Label: label, RawPath: path})
				}
				i += loc[1]
				continue
			}
		}
		i++
	}
	return out
}

// isLocalDocPath rejects targets that obviously aren't local files:
// URLs, mailto:/tel: schemes, in-page anchors. Anything else passes
// through to filesystem validation in dispatchDocRefs.
func isLocalDocPath(p string) bool {
	if p == "" {
		return false
	}
	if strings.Contains(p, "://") {
		return false
	}
	if strings.HasPrefix(p, "mailto:") || strings.HasPrefix(p, "tel:") {
		return false
	}
	if strings.HasPrefix(p, "#") {
		return false
	}
	return true
}
