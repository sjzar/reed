package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/sjzar/reed/internal/model"
)

// ---------------------------------------------------------------------------
// editTool
// ---------------------------------------------------------------------------

type editTool struct{}

func NewEditTool() Tool { return &editTool{} }

func (t *editTool) Group() ToolGroup { return GroupCore }

type editArgs struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
	absPath    string
}

func (t *editTool) Def() model.ToolDef {
	return model.ToolDef{
		Name: "edit",
		Description: "Replace a unique string in an existing file. The old_string must appear exactly once (unless replace_all=true).\n" +
			"Use when: modifying specific code in an existing file. Always read the file first to get the exact text.\n" +
			"Don't use when: creating new files (use write), replacing entire file content (use write). If old_string matches multiple times, include more surrounding context to make it unique, or set replace_all=true for bulk replacement.\n" +
			"Note: old_string must be the raw file content without line number prefixes. Use read with raw=true if copying text for old_string.\n" +
			`Example: {"path": "main.go", "old_string": "func old()", "new_string": "func new()"}`,
		Summary: "Replace a unique string in an existing file. Read the file first.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "File path to edit"},
				"old_string":  map[string]any{"type": "string", "description": "Exact string to find"},
				"new_string":  map[string]any{"type": "string", "description": "Replacement string"},
				"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences instead of requiring unique match (default: false)"},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
	}
}

func (t *editTool) Prepare(ctx context.Context, req CallRequest) (*PreparedCall, error) {
	var a editArgs
	if err := json.Unmarshal(req.RawArgs, &a); err != nil {
		return nil, fmt.Errorf("parse edit args: %w", err)
	}
	if a.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	resolved, err := resolveFSPath(req.Context, a.Path)
	if err != nil {
		return nil, err
	}
	// edit reads the file before writing, so both permissions are required.
	if err := checkReadAccess(ctx, resolved); err != nil {
		return nil, err
	}
	if err := checkWriteAccess(ctx, resolved); err != nil {
		return nil, err
	}
	a.absPath = resolved
	return &PreparedCall{
		ToolCallID: req.ToolCallID,
		Name:       req.Name,
		RawArgs:    req.RawArgs,
		Parsed:     a,
		Plan: ExecutionPlan{
			Mode:    ExecModeSync,
			Timeout: DefaultTimeout,
			Policy:  Scoped,
			Locks:   []ResourceLock{{Key: LockKey(resolved), Mode: LockWrite}},
		},
	}, nil
}

func (t *editTool) Execute(_ context.Context, call *PreparedCall) (*Result, error) {
	a, ok := call.Parsed.(editArgs)
	if !ok {
		return nil, fmt.Errorf("internal: unexpected parsed type %T", call.Parsed)
	}
	data, err := os.ReadFile(a.absPath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	content := string(data)

	// Detect and strip BOM + CRLF before matching
	hasBOM := strings.HasPrefix(content, "\xef\xbb\xbf")
	hasCRLF := strings.Contains(content, "\r\n")

	normalized := content
	if hasBOM {
		normalized = strings.TrimPrefix(normalized, "\xef\xbb\xbf")
	}
	if hasCRLF {
		normalized = strings.ReplaceAll(normalized, "\r\n", "\n")
	}

	// Normalize old_string/new_string too (model may send either style)
	oldNorm := strings.ReplaceAll(a.OldString, "\r\n", "\n")
	newNorm := strings.ReplaceAll(a.NewString, "\r\n", "\n")

	// Guard against empty old_string — would cause strings.ReplaceAll to insert between every rune.
	if oldNorm == "" {
		return nil, fmt.Errorf("old_string must not be empty")
	}

	if oldNorm == newNorm {
		return ErrorResult(fmt.Sprintf(
			"no changes: old_string and new_string are identical in %s", a.absPath)), nil
	}

	count := strings.Count(normalized, oldNorm)
	if count == 0 {
		return nil, fmt.Errorf("old_string not found in %s. Suggestion: Read the file first with raw=true to get exact content including whitespace", a.absPath)
	}

	// Find the line number of the first match (for change context output).
	matchLineIdx := findMatchLineIndex(normalized, oldNorm)

	var newContent string
	if a.ReplaceAll {
		newContent = strings.ReplaceAll(normalized, oldNorm, newNorm)
	} else {
		if count > 1 {
			return nil, fmt.Errorf("old_string found %d times in %s; must be unique. Include more surrounding lines in old_string to make the match unique, or set replace_all=true", count, a.absPath)
		}
		newContent = strings.Replace(normalized, oldNorm, newNorm, 1)
	}

	// Capture normalized new content for preview before BOM/CRLF restoration.
	normalizedNew := newContent

	// Restore BOM
	if hasBOM {
		newContent = "\xef\xbb\xbf" + newContent
	}
	// Restore CRLF
	if hasCRLF {
		newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
	}

	// Preserve file permissions
	perm := fs.FileMode(0o644)
	if info, err := os.Stat(a.absPath); err == nil {
		perm = info.Mode().Perm()
	}

	if err := os.WriteFile(a.absPath, []byte(newContent), perm); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	// Build result with change context.
	var resultMsg strings.Builder
	if a.ReplaceAll && count > 1 {
		fmt.Fprintf(&resultMsg, "replaced %d occurrences in %s\n", count, a.absPath)
	} else {
		fmt.Fprintf(&resultMsg, "edited %s\n", a.absPath)
	}
	resultMsg.WriteString(buildEditContext(normalized, normalizedNew, matchLineIdx))
	return TextResult(resultMsg.String()), nil
}

// findMatchLineIndex returns the 0-based line index of the first occurrence of needle in text.
func findMatchLineIndex(text, needle string) int {
	idx := strings.Index(text, needle)
	if idx < 0 {
		return 0
	}
	return strings.Count(text[:idx], "\n")
}

// buildEditContext produces a before/after diff context for an edit operation.
// Shows up to 3 lines around the first change location.
func buildEditContext(oldContent, newContent string, matchLineIdx int) string {
	const ctxLines = 3

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	// Calculate context range for old content.
	startOld := matchLineIdx - ctxLines
	if startOld < 0 {
		startOld = 0
	}
	endOld := matchLineIdx + ctxLines + 1
	if endOld > len(oldLines) {
		endOld = len(oldLines)
	}

	// For new content, use same start line but adjust end.
	endNew := matchLineIdx + ctxLines + 1
	// The replacement may change number of lines, so scan to find reasonable end.
	lineDiff := len(newLines) - len(oldLines)
	endNew += lineDiff
	if endNew < matchLineIdx+1 {
		endNew = matchLineIdx + 1
	}
	if endNew > len(newLines) {
		endNew = len(newLines)
	}
	startNew := startOld
	if startNew > len(newLines) {
		startNew = len(newLines)
	}

	var b strings.Builder
	b.WriteString("--- before\n")
	b.WriteString(formatLinesWithNumbers(oldLines[startOld:endOld], startOld+1))
	b.WriteString("\n+++ after\n")
	b.WriteString(formatLinesWithNumbers(newLines[startNew:endNew], startNew+1))
	return b.String()
}
