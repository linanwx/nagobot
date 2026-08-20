package tools

// Multi-edit text replacement core, ported from pi-mono's edit-diff.ts.
// Matching tries exact first, then a fuzzy fallback that normalizes trailing
// whitespace, smart quotes, Unicode dashes, special Unicode spaces, and applies
// NFKC. Line endings (CRLF/LF) and a UTF-8 BOM are detected on the original and
// restored after editing; matching always runs in LF-normalized space.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const utf8BOM = "\uFEFF"

// editPair is a single exact-text replacement.
type editPair struct {
	oldText string
	newText string
}

type lineSpan struct {
	start int
	end   int
}

type matchedEdit struct {
	editIndex   int
	matchIndex  int
	matchLength int
	newText     string
}

type fuzzyMatch struct {
	found       bool
	index       int
	matchLength int
	usedFuzzy   bool
}

// fuzzyReplacer maps smart quotes, Unicode dashes, and special Unicode spaces
// to their ASCII equivalents. Applied after NFKC + trailing-whitespace stripping.
var fuzzyReplacer = strings.NewReplacer(
	// Smart single quotes -> '
	"\u2018", "'", "\u2019", "'", "\u201A", "'", "\u201B", "'",
	// Smart double quotes -> "
	"\u201C", "\"", "\u201D", "\"", "\u201E", "\"", "\u201F", "\"",
	// Dashes/hyphens -> -
	"\u2010", "-", "\u2011", "-", "\u2012", "-", "\u2013", "-",
	"\u2014", "-", "\u2015", "-", "\u2212", "-",
	// Special spaces -> regular space
	"\u00A0", " ", "\u2002", " ", "\u2003", " ", "\u2004", " ", "\u2005", " ",
	"\u2006", " ", "\u2007", " ", "\u2008", " ", "\u2009", " ", "\u200A", " ",
	"\u202F", " ", "\u205F", " ", "\u3000", " ",
)

// detectLineEnding reports whether content predominantly uses CRLF or LF,
// based on which appears first.
func detectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 {
		return "\n"
	}
	if crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

// normalizeToLF converts all CRLF and lone CR to LF.
func normalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// restoreLineEndings converts LF back to the file's original ending.
func restoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// stripBom removes a leading UTF-8 BOM, returning the BOM (if any) and the rest.
func stripBom(content string) (bom, text string) {
	if strings.HasPrefix(content, utf8BOM) {
		return utf8BOM, content[len(utf8BOM):]
	}
	return "", content
}

// normalizeForFuzzyMatch applies progressive transformations for fuzzy matching:
// NFKC, per-line trailing-whitespace strip, smart quotes/dashes/spaces to ASCII.
func normalizeForFuzzyMatch(text string) string {
	text = norm.NFKC.String(text)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(l, unicode.IsSpace)
	}
	text = strings.Join(lines, "\n")
	return fuzzyReplacer.Replace(text)
}

// fuzzyFindText finds oldText in content: exact match first, then fuzzy. When
// fuzzy is used the returned index/length are offsets in fuzzy-normalized space.
func fuzzyFindText(content, oldText string) fuzzyMatch {
	if idx := strings.Index(content, oldText); idx != -1 {
		return fuzzyMatch{found: true, index: idx, matchLength: len(oldText), usedFuzzy: false}
	}
	fuzzyContent := normalizeForFuzzyMatch(content)
	fuzzyOld := normalizeForFuzzyMatch(oldText)
	fidx := strings.Index(fuzzyContent, fuzzyOld)
	if fidx == -1 {
		return fuzzyMatch{found: false, index: -1}
	}
	return fuzzyMatch{found: true, index: fidx, matchLength: len(fuzzyOld), usedFuzzy: true}
}

// reconcileEOFTrailingNewlines rewrites an edit whose old_text runs to the end
// of the file but carries a different number of trailing newlines than the file
// actually has.
//
// read_file terminates every rendered line with "\n", so the file's own EOF
// convention is invisible in the output the model copies from: a file ending
// "X" and a file ending "X\n" render identically. Appending is the one thing a
// single exact replacement can only express by anchoring on the last line, so
// that invisible byte sat on the one edit shape that needs it — measured on the
// deployment, 11 of 36 edit_file failures over eight days were an old_text that
// matched the read output verbatim and ran to its very end.
//
// Engaged only when every one of these holds, which is what keeps it from
// silently editing something the model did not point at:
//   - old_text ends with "\n" (otherwise there is nothing to reconcile),
//   - exact AND fuzzy matching already failed (never changes a working edit),
//   - the newline-trimmed old_text sits at the very END of the newline-trimmed
//     content — EOF anchoring is what makes the region unambiguous,
//   - it occurs exactly once, so a model that meant an EARLIER occurrence gets
//     the ordinary not-found/duplicate error rather than a silent edit of the
//     last one.
//
// The file's real trailing newlines are carried over to the replacement, so the
// file keeps its own EOF convention rather than adopting the model's guess. A
// replacement that is only newlines is treated as the deletion it reads as.
func reconcileEOFTrailingNewlines(content string, e editPair) (editPair, bool) {
	oldTrimmed := strings.TrimRight(e.oldText, "\n")
	if oldTrimmed == e.oldText || oldTrimmed == "" {
		return e, false
	}
	contentTrimmed := strings.TrimRight(content, "\n")
	if !strings.HasSuffix(contentTrimmed, oldTrimmed) {
		return e, false
	}
	if strings.Count(contentTrimmed, oldTrimmed) != 1 {
		return e, false
	}
	trail := content[len(contentTrimmed):]
	newTrimmed := strings.TrimRight(e.newText, "\n")
	newText := newTrimmed + trail
	if newTrimmed == "" {
		newText = ""
	}
	return editPair{oldText: oldTrimmed + trail, newText: newText}, true
}

// countOccurrences counts fuzzy-normalized occurrences of oldText in content.
func countOccurrences(content, oldText string) int {
	fuzzyContent := normalizeForFuzzyMatch(content)
	fuzzyOld := normalizeForFuzzyMatch(oldText)
	if fuzzyOld == "" {
		return 0
	}
	return strings.Count(fuzzyContent, fuzzyOld)
}

// splitLinesWithEndings splits content into lines, each retaining its trailing
// "\n" (the final line keeps no "\n" if the content does not end with one).
// An empty string yields an empty slice.
func splitLinesWithEndings(content string) []string {
	if content == "" {
		return []string{}
	}
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

func getLineSpans(content string) []lineSpan {
	lines := splitLinesWithEndings(content)
	spans := make([]lineSpan, len(lines))
	offset := 0
	for i, line := range lines {
		spans[i] = lineSpan{start: offset, end: offset + len(line)}
		offset += len(line)
	}
	return spans
}

// getReplacementLineRange returns the [startLine, endLine) line range (exclusive
// end) that a replacement at matchIndex..matchIndex+matchLength touches.
func getReplacementLineRange(lines []lineSpan, matchIndex, matchLength int) (int, int, error) {
	repStart := matchIndex
	repEnd := matchIndex + matchLength

	startLine := -1
	for i, line := range lines {
		if repStart >= line.start && repStart < line.end {
			startLine = i
			break
		}
	}
	if startLine == -1 {
		return 0, 0, fmt.Errorf("replacement range is outside the base content")
	}

	endLine := startLine
	for endLine < len(lines) && lines[endLine].end < repEnd {
		endLine++
	}
	if endLine >= len(lines) {
		return 0, 0, fmt.Errorf("replacement range is outside the base content")
	}
	return startLine, endLine + 1, nil
}

// applyReplacements applies ascending-sorted replacements to content in reverse
// order so earlier offsets stay valid. offset is subtracted from each matchIndex.
func applyReplacements(content string, reps []matchedEdit, offset int) string {
	result := content
	for i := len(reps) - 1; i >= 0; i-- {
		r := reps[i]
		idx := r.matchIndex - offset
		result = result[:idx] + r.newText + result[idx+r.matchLength:]
	}
	return result
}

// applyReplacementsPreservingUnchangedLines applies replacements matched against
// baseContent (a normalized view) to originalContent, rewriting only the lines a
// replacement touches and copying every other line byte-for-byte from the
// original. Requires originalContent and baseContent to have equal line counts.
func applyReplacementsPreservingUnchangedLines(originalContent, baseContent string, reps []matchedEdit) (string, error) {
	originalLines := splitLinesWithEndings(originalContent)
	baseLines := getLineSpans(baseContent)
	if len(originalLines) != len(baseLines) {
		return "", fmt.Errorf("cannot preserve unchanged lines because the base content has a different line count")
	}

	sorted := append([]matchedEdit(nil), reps...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].matchIndex < sorted[j].matchIndex })

	type group struct {
		startLine int
		endLine   int
		reps      []matchedEdit
	}
	var groups []group
	for _, r := range sorted {
		sl, el, err := getReplacementLineRange(baseLines, r.matchIndex, r.matchLength)
		if err != nil {
			return "", err
		}
		if n := len(groups); n > 0 && sl < groups[n-1].endLine {
			if el > groups[n-1].endLine {
				groups[n-1].endLine = el
			}
			groups[n-1].reps = append(groups[n-1].reps, r)
			continue
		}
		groups = append(groups, group{startLine: sl, endLine: el, reps: []matchedEdit{r}})
	}

	var b strings.Builder
	origIdx := 0
	for _, g := range groups {
		for i := origIdx; i < g.startLine; i++ {
			b.WriteString(originalLines[i])
		}
		groupStart := baseLines[g.startLine].start
		groupEnd := baseLines[g.endLine-1].end
		segment := baseContent[groupStart:groupEnd]
		b.WriteString(applyReplacements(segment, g.reps, groupStart))
		origIdx = g.endLine
	}
	for i := origIdx; i < len(originalLines); i++ {
		b.WriteString(originalLines[i])
	}
	return b.String(), nil
}

// applyEditsToNormalizedContent applies one or more exact-text replacements to
// LF-normalized content. All edits match against the original content (not
// incrementally); each oldText must be non-empty, unique, and non-overlapping.
// Returns the new content and whether any edit needed fuzzy matching.
func applyEditsToNormalizedContent(normalizedContent string, edits []editPair, path string) (string, bool, error) {
	normEdits := make([]editPair, len(edits))
	for i, e := range edits {
		normEdits[i] = editPair{oldText: normalizeToLF(e.oldText), newText: normalizeToLF(e.newText)}
	}

	for i := range normEdits {
		if len(normEdits[i].oldText) == 0 {
			return "", false, emptyOldTextError(path, i, len(normEdits))
		}
	}

	// Reconcile EOF trailing newlines BEFORE deciding whether fuzzy matching is
	// needed: a reconciled edit matches exactly, and must not drag the whole
	// replacement into fuzzy-normalized space (which would rewrite quotes and
	// dashes across every line it touches) over a newline count.
	for i := range normEdits {
		if fuzzyFindText(normalizedContent, normEdits[i].oldText).found {
			continue
		}
		if reconciled, ok := reconcileEOFTrailingNewlines(normalizedContent, normEdits[i]); ok {
			normEdits[i] = reconciled
		}
	}

	usedFuzzy := false
	for _, e := range normEdits {
		if fuzzyFindText(normalizedContent, e.oldText).usedFuzzy {
			usedFuzzy = true
			break
		}
	}

	replacementBase := normalizedContent
	if usedFuzzy {
		replacementBase = normalizeForFuzzyMatch(normalizedContent)
	}

	matched := make([]matchedEdit, 0, len(normEdits))
	for i, e := range normEdits {
		m := fuzzyFindText(replacementBase, e.oldText)
		if !m.found {
			return "", false, notFoundError(path, i, len(normEdits))
		}
		if occ := countOccurrences(replacementBase, e.oldText); occ > 1 {
			return "", false, duplicateError(path, i, len(normEdits), occ)
		}
		matched = append(matched, matchedEdit{editIndex: i, matchIndex: m.index, matchLength: m.matchLength, newText: e.newText})
	}

	sort.SliceStable(matched, func(a, b int) bool { return matched[a].matchIndex < matched[b].matchIndex })
	for i := 1; i < len(matched); i++ {
		prev := matched[i-1]
		cur := matched[i]
		if prev.matchIndex+prev.matchLength > cur.matchIndex {
			return "", false, overlapError(path, prev.editIndex, cur.editIndex)
		}
	}

	var newContent string
	var err error
	if usedFuzzy {
		newContent, err = applyReplacementsPreservingUnchangedLines(normalizedContent, replacementBase, matched)
		if err != nil {
			return "", false, err
		}
	} else {
		newContent = applyReplacements(replacementBase, matched, 0)
	}

	if normalizedContent == newContent {
		return "", false, noChangeError(path, len(normEdits))
	}
	return newContent, usedFuzzy, nil
}

// ErrTextNotFound is the sentinel behind every "could not find the exact text"
// failure. The tool boundary attaches a recovery hint to exactly this case (the
// model has to be shown the real bytes) and to no other, and matching on the
// message text would silently stop working the day the wording changes.
var ErrTextNotFound = errors.New("could not find the exact text")

func notFoundError(path string, editIndex, total int) error {
	if total == 1 {
		return fmt.Errorf("%w in %s; old_text must match exactly including all whitespace and newlines", ErrTextNotFound, path)
	}
	return fmt.Errorf("%w: edits[%d] in %s; old_text must match exactly including all whitespace and newlines", ErrTextNotFound, editIndex, path)
}

func duplicateError(path string, editIndex, total, occurrences int) error {
	if total == 1 {
		return fmt.Errorf("found %d occurrences of the text in %s; the text must be unique, provide more surrounding context", occurrences, path)
	}
	return fmt.Errorf("found %d occurrences of edits[%d] in %s; each old_text must be unique, provide more surrounding context", occurrences, editIndex, path)
}

func emptyOldTextError(path string, editIndex, total int) error {
	if total == 1 {
		return fmt.Errorf("old_text must not be empty in %s", path)
	}
	return fmt.Errorf("edits[%d].old_text must not be empty in %s", editIndex, path)
}

func noChangeError(path string, total int) error {
	if total == 1 {
		return fmt.Errorf("no changes made to %s; the replacement produced identical content (check for special characters or that the text exists as expected)", path)
	}
	return fmt.Errorf("no changes made to %s; the replacements produced identical content", path)
}

func overlapError(path string, prevIndex, curIndex int) error {
	return fmt.Errorf("edits[%d] and edits[%d] overlap in %s; merge them into one edit or target disjoint regions", prevIndex, curIndex, path)
}
