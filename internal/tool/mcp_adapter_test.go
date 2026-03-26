package tool

import (
	"context"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

type mockToolPool struct {
	tools  []model.ToolDef
	callFn func(ctx context.Context, serverID, toolName string, args map[string]any) (*model.ToolResult, error)
}

func (m *mockToolPool) ListAllTools(_ context.Context) ([]model.ToolDef, error) {
	return m.tools, nil
}

func (m *mockToolPool) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (*model.ToolResult, error) {
	if m.callFn != nil {
		return m.callFn(ctx, serverID, toolName, args)
	}
	return &model.ToolResult{Content: []model.Content{{Type: "text", Text: "mock result"}}}, nil
}

func TestWrapMCPTools(t *testing.T) {
	pool := &mockToolPool{
		tools: []model.ToolDef{
			{Name: "server1__search", Description: "Search"},
			{Name: "read_file", Description: "Read a file"},
		},
	}

	tools, err := WrapMCPTools(context.Background(), pool)
	if err != nil {
		t.Fatalf("WrapMCPTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools count: got %d", len(tools))
	}
	if tools[0].Def().Name != "server1__search" {
		t.Errorf("tool[0] name: got %q", tools[0].Def().Name)
	}
}

func TestMCPToolExecute(t *testing.T) {
	pool := &mockToolPool{
		tools: []model.ToolDef{{Name: "test_tool", Description: "Test"}},
		callFn: func(_ context.Context, _, toolName string, _ map[string]any) (*model.ToolResult, error) {
			if toolName != "test_tool" {
				t.Errorf("toolName: got %q", toolName)
			}
			return &model.ToolResult{Content: []model.Content{{Type: "text", Text: "executed"}}}, nil
		},
	}

	tools, _ := WrapMCPTools(context.Background(), pool)
	prepared, err := tools[0].Prepare(context.Background(), CallRequest{
		ToolCallID: "tc1", Name: "test_tool", RawArgs: []byte(`{"q":"test"}`),
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	result, err := tools[0].Execute(context.Background(), prepared)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "executed" {
		t.Errorf("result: got %+v", result)
	}
}

func TestMCPToolIsError(t *testing.T) {
	pool := &mockToolPool{
		tools: []model.ToolDef{{Name: "err_tool", Description: "Error"}},
		callFn: func(_ context.Context, _, _ string, _ map[string]any) (*model.ToolResult, error) {
			return &model.ToolResult{
				Content: []model.Content{{Type: "text", Text: "failed"}},
				IsError: true,
			}, nil
		},
	}

	tools, _ := WrapMCPTools(context.Background(), pool)
	prepared, _ := tools[0].Prepare(context.Background(), CallRequest{
		ToolCallID: "tc1", Name: "err_tool", RawArgs: []byte(`{}`),
	})
	result, err := tools[0].Execute(context.Background(), prepared)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
}

func TestMCPToolNonTextBlock(t *testing.T) {
	pool := &mockToolPool{
		tools: []model.ToolDef{{Name: "img_tool", Description: "Image"}},
		callFn: func(_ context.Context, _, _ string, _ map[string]any) (*model.ToolResult, error) {
			return &model.ToolResult{
				Content: []model.Content{
					{Type: "text", Text: "hello"},
					{Type: model.ContentTypeImage, MediaURI: "data:image/png;base64,abc", MIMEType: "image/png"},
					{Type: "video", Text: ""},
				},
			}, nil
		},
	}

	tools, _ := WrapMCPTools(context.Background(), pool)
	prepared, _ := tools[0].Prepare(context.Background(), CallRequest{
		ToolCallID: "tc1", Name: "img_tool", RawArgs: []byte(`{}`),
	})
	result, _ := tools[0].Execute(context.Background(), prepared)
	if len(result.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(result.Content))
	}
	// First block: text passthrough
	if result.Content[0].Type != model.ContentTypeText || result.Content[0].Text != "hello" {
		t.Fatalf("expected text block, got %+v", result.Content[0])
	}
	// Second block: image passthrough (not replaced with placeholder)
	if result.Content[1].Type != model.ContentTypeImage {
		t.Fatalf("expected image block passthrough, got type %q", result.Content[1].Type)
	}
	if result.Content[1].MediaURI != "data:image/png;base64,abc" {
		t.Fatalf("expected image MediaURI preserved, got %q", result.Content[1].MediaURI)
	}
	// Third block: unknown type becomes text placeholder
	if result.Content[2].Text != "[non-text content: video]" {
		t.Fatalf("expected non-text placeholder for unknown type, got %q", result.Content[2].Text)
	}
}

func TestMCPToolEmptyArgs(t *testing.T) {
	pool := &mockToolPool{
		tools: []model.ToolDef{{Name: "no_args", Description: "No args"}},
	}
	tools, _ := WrapMCPTools(context.Background(), pool)
	prepared, err := tools[0].Prepare(context.Background(), CallRequest{
		ToolCallID: "tc1", Name: "no_args",
	})
	if err != nil {
		t.Fatalf("prepare with empty args: %v", err)
	}
	if prepared.Plan.Policy != ParallelSafe {
		t.Fatal("expected ParallelSafe default policy")
	}
}

func TestMCPTool_PrefixedName_RoundTrip(t *testing.T) {
	var gotServerID, gotToolName string
	pool := &mockToolPool{
		tools: []model.ToolDef{
			{Name: "s1__read", Description: "Read"},
		},
		callFn: func(_ context.Context, serverID, toolName string, _ map[string]any) (*model.ToolResult, error) {
			gotServerID = serverID
			gotToolName = toolName
			return &model.ToolResult{Content: []model.Content{{Type: "text", Text: "ok"}}}, nil
		},
	}

	tools, err := WrapMCPTools(context.Background(), pool)
	if err != nil {
		t.Fatalf("WrapMCPTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(tools))
	}

	prepared, err := tools[0].Prepare(context.Background(), CallRequest{
		ToolCallID: "tc1", Name: "s1__read", RawArgs: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	_, err = tools[0].Execute(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if gotServerID != "s1" {
		t.Errorf("serverID: got %q, want %q", gotServerID, "s1")
	}
	if gotToolName != "read" {
		t.Errorf("toolName: got %q, want %q", gotToolName, "read")
	}
}

func TestParseToolName(t *testing.T) {
	tests := []struct {
		input    string
		serverID string
		toolName string
	}{
		{"server__tool", "server", "tool"},
		{"simple_tool", "", "simple_tool"},
		{"a__b__c", "a", "b__c"},
	}
	for _, tt := range tests {
		sid, tn := ParseToolName(tt.input)
		if sid != tt.serverID || tn != tt.toolName {
			t.Errorf("ParseToolName(%q): got (%q, %q), want (%q, %q)", tt.input, sid, tn, tt.serverID, tt.toolName)
		}
	}
}
