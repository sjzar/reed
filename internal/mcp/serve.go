package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sjzar/reed/internal/model"
)

// ToolCallHandler handles MCP tool call dispatch.
type ToolCallHandler func(ctx context.Context, toolName string, arguments map[string]any) (map[string]any, error)

// NewMCPServer creates a go-sdk Server with tools from workflow MCP definitions.
func NewMCPServer(tools []model.MCPTool, handler ToolCallHandler) *gosdk.Server {
	server := gosdk.NewServer(&gosdk.Implementation{
		Name: "reed", Version: "0.1.0",
	}, nil)

	for _, t := range tools {
		tool := t
		server.AddTool(&gosdk.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: buildInputSchema(tool.Inputs),
		}, func(ctx context.Context, req *gosdk.CallToolRequest) (*gosdk.CallToolResult, error) {
			// Parse raw JSON arguments into map
			var args map[string]any
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return &gosdk.CallToolResult{
						IsError: true,
						Content: []gosdk.Content{&gosdk.TextContent{Text: fmt.Sprintf("invalid arguments: %v", err)}},
					}, nil
				}
			}
			if args == nil {
				args = make(map[string]any)
			}

			outputs, err := handler(ctx, req.Params.Name, args)
			if err != nil {
				return &gosdk.CallToolResult{
					IsError: true,
					Content: []gosdk.Content{&gosdk.TextContent{Text: err.Error()}},
				}, nil
			}
			data, _ := json.Marshal(outputs)
			return &gosdk.CallToolResult{
				Content: []gosdk.Content{&gosdk.TextContent{Text: string(data)}},
			}, nil
		})
	}
	return server
}

// NewStreamableHandler wraps a Server as a Streamable HTTP handler.
func NewStreamableHandler(server *gosdk.Server) http.Handler {
	return gosdk.NewStreamableHTTPHandler(
		func(_ *http.Request) *gosdk.Server { return server },
		&gosdk.StreamableHTTPOptions{Stateless: true},
	)
}

// buildInputSchema converts workflow InputSpec map to a JSON Schema object.
func buildInputSchema(inputs map[string]model.InputSpec) map[string]any {
	schema := map[string]any{
		"type": "object",
	}
	if len(inputs) == 0 {
		return schema
	}

	properties := make(map[string]any, len(inputs))
	var required []string
	for name, spec := range inputs {
		prop := map[string]any{}
		if spec.Type != "" {
			prop["type"] = spec.Type
		}
		if spec.Description != "" {
			prop["description"] = spec.Description
		}
		properties[name] = prop
		if spec.Required {
			required = append(required, name)
		}
	}
	schema["properties"] = properties
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
