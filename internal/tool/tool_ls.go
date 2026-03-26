package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sjzar/reed/internal/model"
)

// DefaultLSLimit is the maximum number of directory entries returned by the ls tool.
const DefaultLSLimit = 1000

// ---------------------------------------------------------------------------
// lsTool
// ---------------------------------------------------------------------------

type lsTool struct{}

func NewLSTool() Tool { return &lsTool{} }

func (t *lsTool) Group() ToolGroup { return GroupCore }

type lsArgs struct {
	Path    string `json:"path"`
	absPath string
}

func (t *lsTool) Def() model.ToolDef {
	return model.ToolDef{
		Name: "ls",
		Description: "List directory contents (files and subdirectories).\n" +
			"Use when: exploring directory structure, checking what files exist in a directory.\n" +
			"Don't use when: searching for files or content (use search).",
		Summary: "List directory contents.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Directory path (optional, defaults to cwd)"},
			},
		},
	}
}

func (t *lsTool) Prepare(ctx context.Context, req CallRequest) (*PreparedCall, error) {
	var a lsArgs
	if err := json.Unmarshal(req.RawArgs, &a); err != nil {
		return nil, fmt.Errorf("parse ls args: %w", err)
	}
	if a.Path == "" {
		a.Path = req.Context.Cwd
	}
	if a.Path == "" {
		return nil, fmt.Errorf("path is required (no cwd available)")
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
			Policy:  ParallelSafe,
		},
	}, nil
}

func (t *lsTool) Execute(ctx context.Context, call *PreparedCall) (*Result, error) {
	a, ok := call.Parsed.(lsArgs)
	if !ok {
		return nil, fmt.Errorf("internal: unexpected parsed type %T", call.Parsed)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	entries, err := os.ReadDir(a.absPath)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	// Sort alphabetically
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	truncated := false
	if len(entries) > DefaultLSLimit {
		entries = entries[:DefaultLSLimit]
		truncated = true
	}

	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name)
		b.WriteByte('\n')
	}
	if truncated {
		fmt.Fprintf(&b, "\n[truncated: showing %d entries, more available]", DefaultLSLimit)
	}
	return TextResult(b.String()), nil
}
