package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sjzar/reed/internal/model"
)

// ToolPool abstracts the MCP tool pool for tool adaptation.
type ToolPool interface {
	ListAllTools(ctx context.Context) ([]model.ToolDef, error)
	CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (*model.ToolResult, error)
}

// mcpTool adapts a single MCP tool to the Tool interface.
type mcpTool struct {
	def      model.ToolDef
	pool     ToolPool
	serverID string
	toolName string
}

func (m *mcpTool) Def() model.ToolDef { return m.def }

func (m *mcpTool) Prepare(_ context.Context, req CallRequest) (*PreparedCall, error) {
	var parsed map[string]any
	if len(req.RawArgs) > 0 {
		if err := json.Unmarshal(req.RawArgs, &parsed); err != nil {
			return nil, fmt.Errorf("parse mcp tool args: %w", err)
		}
	}
	return &PreparedCall{
		ToolCallID: req.ToolCallID,
		Name:       req.Name,
		RawArgs:    req.RawArgs,
		Parsed:     parsed,
		Plan: ExecutionPlan{
			Mode:   ExecModeSync,
			Policy: ParallelSafe,
		},
	}, nil
}

func (m *mcpTool) Execute(ctx context.Context, call *PreparedCall) (*Result, error) {
	var args map[string]any
	if call.Parsed != nil {
		var ok bool
		args, ok = call.Parsed.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("internal: unexpected parsed type %T", call.Parsed)
		}
	}
	result, err := m.pool.CallTool(ctx, m.serverID, m.toolName, args)
	if err != nil {
		return nil, err
	}

	content := make([]model.Content, 0, len(result.Content))
	for _, block := range result.Content {
		switch block.Type {
		case model.ContentTypeText, "":
			content = append(content, model.Content{
				Type: model.ContentTypeText,
				Text: block.Text,
			})
		case model.ContentTypeImage:
			content = append(content, block)
		default:
			content = append(content, model.Content{
				Type: model.ContentTypeText,
				Text: fmt.Sprintf("[non-text content: %s]", block.Type),
			})
		}
	}

	return &Result{
		Content: content,
		IsError: result.IsError,
	}, nil
}

// WrapMCPTools converts all tools from a ToolPool into Tool instances.
func WrapMCPTools(ctx context.Context, pool ToolPool) ([]Tool, error) {
	defs, err := pool.ListAllTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("list mcp tools: %w", err)
	}
	tools := make([]Tool, 0, len(defs))
	for _, def := range defs {
		serverID, toolName := ParseToolName(def.Name)
		tools = append(tools, &mcpTool{
			def: def, pool: pool, serverID: serverID, toolName: toolName,
		})
	}
	return tools, nil
}

// ParseToolName splits a potentially prefixed tool name into serverID and toolName.
func ParseToolName(name string) (serverID, toolName string) {
	if before, after, ok := strings.Cut(name, "__"); ok {
		return before, after
	}
	return "", name
}
