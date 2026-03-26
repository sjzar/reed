// Package truncate provides text truncation by line count and byte size.
package truncate

import (
	"strings"
	"unicode/utf8"
)

// Info describes the result of a truncation operation.
//
// Line counting uses strings.Split(text, "\n"), so an empty string counts as
// one line and a trailing newline adds an empty final line. CRLF sequences are
// not normalised — callers working with Windows-style line endings should
// convert to LF before calling the truncation functions.
type Info struct {
	Truncated    bool
	TotalLines   int
	TotalBytes   int
	ShownLines   int
	ByteLimitHit bool // true when byte-level truncation produced a placeholder instead of content
}

// Head keeps the first maxLines/maxBytes of text (head truncation).
// Returns the truncated text and info. Zero maxLines/maxBytes means no limit for that dimension.
//
// When byte truncation cuts mid-line, the result snaps back to the last complete line.
// If no newline is found (a single line exceeds the byte limit), a placeholder string
// "[content truncated: single line exceeds byte limit]" is returned and ByteLimitHit is set.
func Head(text string, maxLines, maxBytes int) (string, Info) {
	lines := strings.Split(text, "\n")
	info := Info{
		TotalLines: len(lines),
		TotalBytes: len(text),
		ShownLines: len(lines),
	}

	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		info.Truncated = true
	}

	result := strings.Join(lines, "\n")
	if maxBytes > 0 && len(result) > maxBytes {
		result = truncateBytesSafeHead(result, maxBytes)
		info.Truncated = true
		if strings.HasPrefix(result, "[content truncated:") {
			info.ShownLines = 0
			info.ByteLimitHit = true
			return result, info
		}
		lines = strings.Split(result, "\n")
	}

	info.ShownLines = len(lines)
	return result, info
}

// Tail keeps the last maxLines/maxBytes of text (tail truncation).
// Returns the truncated text and info. Zero maxLines/maxBytes means no limit for that dimension.
//
// When byte truncation cuts mid-line, the result snaps forward to the first complete line.
// If no newline is found (a single line exceeds the byte limit), a placeholder string
// "[content truncated: single line exceeds byte limit]" is returned and ByteLimitHit is set.
func Tail(text string, maxLines, maxBytes int) (string, Info) {
	lines := strings.Split(text, "\n")
	info := Info{
		TotalLines: len(lines),
		TotalBytes: len(text),
		ShownLines: len(lines),
	}

	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
		info.Truncated = true
	}

	result := strings.Join(lines, "\n")
	if maxBytes > 0 && len(result) > maxBytes {
		result = truncateBytesSafeTail(result, maxBytes)
		info.Truncated = true
		if strings.HasPrefix(result, "[content truncated:") {
			info.ShownLines = 0
			info.ByteLimitHit = true
			return result, info
		}
		lines = strings.Split(result, "\n")
	}

	info.ShownLines = len(lines)
	return result, info
}

// truncateBytesSafeHead truncates to maxBytes, snapping back to the last complete line.
// If no newline is found (single line exceeds maxBytes), returns a placeholder.
func truncateBytesSafeHead(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if idx := strings.LastIndex(s[:cut], "\n"); idx >= 0 {
		return s[:idx]
	}
	return "[content truncated: single line exceeds byte limit]"
}

// truncateBytesSafeTail truncates from the front to maxBytes, snapping forward to the first complete line.
// If no newline is found (single line exceeds maxBytes), returns a placeholder.
func truncateBytesSafeTail(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	if idx := strings.Index(s[start:], "\n"); idx >= 0 {
		return s[start+idx+1:]
	}
	return "[content truncated: single line exceeds byte limit]"
}
