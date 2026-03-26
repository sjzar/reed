package tool

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/pkg/truncate"
)

const (
	DefaultMaxLines = 2000
	DefaultMaxBytes = 50 * 1024

	// MaxOutputBytes is the service-level output size cap applied to all tools.
	MaxOutputBytes = 100 * 1024
	// MaxOutputLines is the service-level output line cap applied to all tools.
	MaxOutputLines = 2000
)

// sanitizeResult applies universal safety checks to any tool result.
// Called by Service.runWithPlan after tool execution, before wrapping into CallResult.
// Handles: nil result, binary content detection, output size/line truncation.
func sanitizeResult(r *Result) *Result {
	if r == nil {
		return ErrorResult("tool returned nil result")
	}
	for i, c := range r.Content {
		if c.Type != model.ContentTypeText || c.Text == "" {
			continue
		}
		text := c.Text

		// Binary detection: check first 512 bytes for null byte.
		peek := text
		if len(peek) > 512 {
			peek = peek[:512]
		}
		if bytes.ContainsRune([]byte(peek), 0) {
			r.Content[i].Text = fmt.Sprintf("[binary content detected: %d bytes]", len(text))
			continue
		}

		// Size + line truncation.
		if len(text) > MaxOutputBytes || strings.Count(text, "\n")+1 > MaxOutputLines {
			truncated, info := truncate.Tail(text, MaxOutputLines, MaxOutputBytes)
			if info.Truncated {
				if info.ByteLimitHit && info.ShownLines == 0 {
					truncated = "[output exceeds size limit]\n" + truncated
				} else {
					truncated = fmt.Sprintf("[output truncated: showing last %d of %d lines]\n%s", info.ShownLines, info.TotalLines, truncated)
				}
			}
			r.Content[i].Text = truncated
		}
	}
	return r
}
