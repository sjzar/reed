package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sjzar/reed/internal/media"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/pkg/mimetype"
	"github.com/sjzar/reed/pkg/truncate"
)

// ---------------------------------------------------------------------------
// readTool
// ---------------------------------------------------------------------------

type readTool struct{}

func NewReadTool() Tool { return &readTool{} }

func (t *readTool) Group() ToolGroup { return GroupCore }

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
	Raw    bool   `json:"raw"`
	// resolved absolute path (set during Prepare)
	absPath string
}

func (t *readTool) Def() model.ToolDef {
	return model.ToolDef{
		Name: "read",
		Description: "Read file contents with line numbers, optional offset and limit.\n" +
			"Use when: viewing source code, config files, text files, images, or PDF documents.\n" +
			"For images (PNG/JPG/GIF/WebP): returns the image so you can see it.\n" +
			"For PDFs: returns the document content.\n" +
			"Don't use when: searching for a pattern across many files (use search), listing directory contents (use ls).\n" +
			`Example: {"path": "main.go"} or {"path": "screenshot.png"}`,
		Summary: "Read files: text with line numbers, images/PDFs as visual content.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "File path to read"},
				"offset": map[string]any{"type": "integer", "description": "Line offset to start reading from (0-based, optional)"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum number of lines to read (optional)"},
				"raw":    map[string]any{"type": "boolean", "description": "Output without line numbers (default: false)"},
			},
			"required": []string{"path"},
		},
	}
}

func (t *readTool) Prepare(ctx context.Context, req CallRequest) (*PreparedCall, error) {
	var a readArgs
	if err := json.Unmarshal(req.RawArgs, &a); err != nil {
		return nil, fmt.Errorf("parse read args: %w", err)
	}
	if a.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	resolved, err := resolveFSPath(req.Context, a.Path)
	if err != nil {
		return nil, err
	}
	if err := checkReadAccess(ctx, resolved); err != nil {
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
			Locks:   []ResourceLock{{Key: LockKey(resolved), Mode: LockRead}},
		},
	}, nil
}

func (t *readTool) Execute(ctx context.Context, call *PreparedCall) (*Result, error) {
	a, ok := call.Parsed.(readArgs)
	if !ok {
		return nil, fmt.Errorf("internal: unexpected parsed type %T", call.Parsed)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	data, err := os.ReadFile(a.absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s. Suggestion: Use search with glob to find the correct file path", a.absPath)
		}
		return nil, fmt.Errorf("read file: %w", err)
	}

	// Known binary types by extension — route to media result even if content
	// appears text-like (e.g., PDFs start with %PDF- which has no NUL bytes).
	extMIME := mimetype.DetectFromPath(a.absPath)
	if mimetype.IsImage(extMIME) || mimetype.IsPDF(extMIME) {
		return readBinaryResult(a.absPath, data), nil
	}

	// Generic binary detection: check first 512 bytes for null byte
	peek := data
	if len(peek) > 512 {
		peek = peek[:512]
	}
	if bytes.ContainsRune(peek, 0) {
		return readBinaryResult(a.absPath, data), nil
	}

	lines := strings.Split(string(data), "\n")
	startLine := 1 // 1-based line number for display
	if a.Offset > 0 {
		if a.Offset >= len(lines) {
			return nil, fmt.Errorf("offset %d exceeds file length (%d lines). Use offset=0 or a smaller value", a.Offset, len(lines))
		}
		startLine = a.Offset + 1
		lines = lines[a.Offset:]
	}
	if a.Limit > 0 && a.Limit < len(lines) {
		lines = lines[:a.Limit]
	}

	var content string
	if a.Raw {
		content = strings.Join(lines, "\n")
	} else {
		content = formatLinesWithNumbers(lines, startLine)
	}
	truncated, info := truncate.Head(content, DefaultMaxLines, DefaultMaxBytes)
	if info.Truncated {
		if info.ByteLimitHit && info.ShownLines == 0 {
			truncated += "\n\n[file content exceeds size limit. Use offset/limit to read in smaller chunks.]"
		} else {
			truncated += fmt.Sprintf("\n\n[truncated: showing %d of %d lines. Use offset/limit to read more.]", info.ShownLines, info.TotalLines)
		}
	}
	return TextResult(truncated), nil
}

// readBinaryResult returns appropriate content blocks for binary files.
// Images → image content block (LLM can see the image)
// PDFs → document content block
// Other → text description with file info
func readBinaryResult(absPath string, data []byte) *Result {
	if len(data) > media.MaxMediaSize {
		return TextResult(fmt.Sprintf("binary file too large: %s (%d bytes, max %d)", absPath, len(data), media.MaxMediaSize))
	}

	mimeType := mimetype.Detect(absPath, data)

	encoded := base64.StdEncoding.EncodeToString(data)
	dataURI := "data:" + mimeType + ";base64," + encoded

	if mimetype.IsImage(mimeType) {
		return &Result{
			Content: []model.Content{
				{Type: model.ContentTypeText, Text: fmt.Sprintf("[read: %s (%d bytes)]", filepath.Base(absPath), len(data))},
				{Type: model.ContentTypeImage, MediaURI: dataURI, MIMEType: mimeType},
			},
		}
	}

	if mimetype.IsPDF(mimeType) {
		return &Result{
			Content: []model.Content{
				{Type: model.ContentTypeText, Text: fmt.Sprintf("[read: %s (%d bytes)]", filepath.Base(absPath), len(data))},
				{Type: model.ContentTypeDocument, MediaURI: dataURI, MIMEType: mimeType, Filename: filepath.Base(absPath)},
			},
		}
	}

	// Unknown binary: text description only
	return TextResult(fmt.Sprintf("binary file: %s (%d bytes, %s)", absPath, len(data), mimeType))
}
