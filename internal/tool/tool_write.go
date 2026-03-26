package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sjzar/reed/internal/model"
)

// ---------------------------------------------------------------------------
// writeTool
// ---------------------------------------------------------------------------

type writeTool struct{}

func NewWriteTool() Tool { return &writeTool{} }

func (t *writeTool) Group() ToolGroup { return GroupCore }

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	absPath string
}

func (t *writeTool) Def() model.ToolDef {
	return model.ToolDef{
		Name: "write",
		Description: "Create a new file or completely overwrite an existing file. Creates parent directories automatically.\n" +
			"Use when: creating new files, or replacing the entire content of an existing file.\n" +
			"Don't use when: modifying a specific part of an existing file (use edit — it's safer and preserves the rest).",
		Summary: "Create or overwrite a file.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "File path to write"},
				"content": map[string]any{"type": "string", "description": "Content to write"},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t *writeTool) Prepare(ctx context.Context, req CallRequest) (*PreparedCall, error) {
	var a writeArgs
	if err := json.Unmarshal(req.RawArgs, &a); err != nil {
		return nil, fmt.Errorf("parse write args: %w", err)
	}
	if a.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	resolved, err := resolveFSPath(req.Context, a.Path)
	if err != nil {
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

func (t *writeTool) Execute(_ context.Context, call *PreparedCall) (*Result, error) {
	a, ok := call.Parsed.(writeArgs)
	if !ok {
		return nil, fmt.Errorf("internal: unexpected parsed type %T", call.Parsed)
	}

	// Preserve existing file permissions
	perm := fs.FileMode(0o644)
	if info, err := os.Stat(a.absPath); err == nil {
		perm = info.Mode().Perm()
	}

	if err := os.MkdirAll(filepath.Dir(a.absPath), 0o755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}
	if err := os.WriteFile(a.absPath, []byte(a.Content), perm); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}
	return TextResult(fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.absPath)), nil
}
