package thread

import (
	"context"
	"strings"

	"github.com/linanwx/nagobot/logger"
)

// MarkdownStreamer buffers text deltas from streaming LLM generation and
// sends complete markdown blocks to a Sink as they become available.
type MarkdownStreamer struct {
	sink      Sink
	ctx       context.Context
	buf       strings.Builder
	sent      int  // byte offset of content already sent
	threshold int  // minimum unsent bytes before attempting a split
	active    bool // whether any content has been streamed
}

// NewMarkdownStreamer creates a streamer that sends markdown blocks to sink
// once the unsent buffer exceeds threshold bytes.
func NewMarkdownStreamer(sink Sink, ctx context.Context, threshold int) *MarkdownStreamer {
	return &MarkdownStreamer{
		sink:      sink,
		ctx:       ctx,
		threshold: threshold,
	}
}

// OnDelta is the callback for Runner.OnText. It accumulates text and
// sends complete markdown blocks when the buffer is large enough.
func (s *MarkdownStreamer) OnDelta(delta string) {
	s.buf.WriteString(delta)

	unsent := s.buf.Len() - s.sent
	if unsent < s.threshold {
		return
	}

	text := s.buf.String()[s.sent:]
	splitPos := findMarkdownSplit(text)
	if splitPos <= 0 {
		return
	}

	chunk := text[:splitPos]
	if err := s.sink.Send(s.ctx, chunk); err != nil {
		logger.Error("streamer send error", "err", err)
		return
	}
	s.sent += splitPos
	s.active = true
}

// Flush sends any remaining unsent content and resets the buffer
// for the next provider.Chat() call.
func (s *MarkdownStreamer) Flush() {
	remaining := s.buf.String()[s.sent:]
	if remaining != "" {
		if err := s.sink.Send(s.ctx, remaining); err != nil {
			logger.Error("streamer flush error", "err", err)
		} else {
			s.active = true
		}
	}
	s.buf.Reset()
	s.sent = 0
}

// Streamed returns true if any content was sent via streaming.
func (s *MarkdownStreamer) Streamed() bool {
	return s.active
}

// findMarkdownSplit finds a suitable split position in text, returning the
// byte offset to split at. Returns -1 if no good split point exists.
//
// Strategy: scan backward for paragraph boundaries (\n\n) or heading
// boundaries (\n#), avoiding splits inside code blocks, tables, or lists.
func findMarkdownSplit(text string) int {
	if len(text) == 0 {
		return -1
	}

	// Count open code fences to detect if we're inside a code block.
	// We only consider splitting at positions where fences are balanced.
	fenceCount := 0
	fencePositions := make([]int, 0) // positions where fence count changes

	lines := strings.Split(text, "\n")
	lineOffsets := make([]int, len(lines))
	offset := 0
	for i, line := range lines {
		lineOffsets[i] = offset
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			fenceCount++
			fencePositions = append(fencePositions, i)
		}
		offset += len(line) + 1 // +1 for \n
	}

	// If we're inside an unclosed code block, don't split at all.
	if fenceCount%2 != 0 {
		return -1
	}

	// Find the best split point by scanning lines backward.
	// Prefer: heading boundaries > paragraph boundaries (blank lines).
	bestSplit := -1

	for i := len(lines) - 1; i >= 1; i-- {
		lineStart := lineOffsets[i]
		if lineStart == 0 {
			continue
		}

		// Check we're not inside a code block at this line.
		fencesBeforeLine := 0
		for _, fp := range fencePositions {
			if fp < i {
				fencesBeforeLine++
			}
		}
		if fencesBeforeLine%2 != 0 {
			continue // inside a code block
		}

		trimmed := strings.TrimSpace(lines[i])
		prevTrimmed := strings.TrimSpace(lines[i-1])

		// Heading boundary: current line starts with #
		if strings.HasPrefix(trimmed, "#") {
			// Don't split if we're in the middle of a table or list block.
			if isTableLine(prevTrimmed) || isContinuousList(lines, i) {
				continue
			}
			bestSplit = lineStart
			break
		}

		// Paragraph boundary: blank line
		if trimmed == "" && prevTrimmed != "" {
			// Check: the blank line shouldn't be inside a list or table.
			if i+1 < len(lines) && isTableLine(strings.TrimSpace(lines[i+1])) {
				continue
			}
			// Split after the blank line (include it in the first chunk).
			splitAt := lineStart + len(lines[i]) + 1
			if splitAt <= len(text) {
				bestSplit = splitAt
				break
			}
		}
	}

	return bestSplit
}

func isTableLine(s string) bool {
	return strings.HasPrefix(s, "|") || strings.HasPrefix(s, "|-")
}

func isContinuousList(lines []string, idx int) bool {
	if idx <= 0 {
		return false
	}
	prev := strings.TrimSpace(lines[idx-1])
	return strings.HasPrefix(prev, "- ") || strings.HasPrefix(prev, "* ") ||
		strings.HasPrefix(prev, "+ ") || (len(prev) > 2 && prev[0] >= '0' && prev[0] <= '9' && strings.Contains(prev[:3], "."))
}
