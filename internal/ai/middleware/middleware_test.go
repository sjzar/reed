package middleware

import (
	"context"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestDeepCopyRequest_MessagesIsolated(t *testing.T) {
	original := &model.Request{
		Model: "test",
		Messages: []model.Message{
			{
				Role: model.RoleAssistant,
				Content: []model.Content{
					{Type: model.ContentTypeThinking, Text: "thinking", Signature: "sig"},
					{Type: model.ContentTypeText, Text: "hello"},
				},
				ToolCalls: []model.ToolCall{
					{ID: "tc1", Name: "greet", Arguments: map[string]any{"name": "world"}},
				},
			},
		},
		Tools: []model.ToolDef{
			{Name: "greet", Description: "Greet someone", InputSchema: map[string]any{"type": "object"}},
		},
		Stop:        []string{"stop1"},
		Schema:      map[string]any{"type": "object"},
		ExtraParams: map[string]any{"key": "value"},
	}

	clone := deepCopyRequest(original)

	// Mutate clone — original should be unaffected
	clone.Messages[0].Content[0].Type = model.ContentTypeText
	clone.Messages[0].Content[0].Signature = ""
	clone.Messages[0].ToolCalls[0].Name = "mutated"
	clone.Messages[0].ToolCalls[0].Arguments["name"] = "mutated"
	clone.Tools[0].Name = "mutated"
	clone.Tools[0].InputSchema["type"] = "mutated"
	clone.Stop[0] = "mutated"
	clone.Schema["type"] = "mutated"
	clone.ExtraParams["key"] = "mutated"

	// Verify original is untouched
	if original.Messages[0].Content[0].Type != model.ContentTypeThinking {
		t.Error("Content type was mutated")
	}
	if original.Messages[0].Content[0].Signature != "sig" {
		t.Error("Signature was mutated")
	}
	if original.Messages[0].ToolCalls[0].Name != "greet" {
		t.Error("ToolCall name was mutated")
	}
	if original.Messages[0].ToolCalls[0].Arguments["name"] != "world" {
		t.Error("ToolCall arguments were mutated")
	}
	if original.Tools[0].Name != "greet" {
		t.Error("Tool name was mutated")
	}
	if original.Tools[0].InputSchema["type"] != "object" {
		t.Error("Tool InputSchema was mutated")
	}
	if original.Stop[0] != "stop1" {
		t.Error("Stop was mutated")
	}
	if original.Schema["type"] != "object" {
		t.Error("Schema was mutated")
	}
	if original.ExtraParams["key"] != "value" {
		t.Error("ExtraParams was mutated")
	}
}

func TestDeepCopyRequest_NestedMapsIsolated(t *testing.T) {
	original := &model.Request{
		Model: "test",
		Tools: []model.ToolDef{
			{
				Name:        "greet",
				Description: "Greet someone",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "The name",
						},
					},
					"required": []string{"name"},
					"enum":     []string{"hello", "world"},
				},
			},
		},
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"result": map[string]any{"type": "string"},
			},
		},
		ExtraParams: map[string]any{
			"nested": map[string]any{"deep": "value"},
			"list":   []any{"a", map[string]any{"b": "c"}},
		},
		Messages: []model.Message{
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "tc1", Name: "greet", Arguments: map[string]any{
						"nested": map[string]any{"key": "original"},
					}},
				},
			},
		},
	}

	clone := deepCopyRequest(original)

	// Mutate nested maps in clone
	clone.Tools[0].InputSchema["properties"].(map[string]any)["name"].(map[string]any)["type"] = "integer"
	clone.Tools[0].InputSchema["required"].([]string)[0] = "mutated"
	clone.Tools[0].InputSchema["enum"].([]string)[0] = "mutated"
	clone.Schema["properties"].(map[string]any)["result"].(map[string]any)["type"] = "integer"
	clone.ExtraParams["nested"].(map[string]any)["deep"] = "mutated"
	clone.ExtraParams["list"].([]any)[1].(map[string]any)["b"] = "mutated"
	clone.Messages[0].ToolCalls[0].Arguments["nested"].(map[string]any)["key"] = "mutated"

	// Verify originals untouched
	if original.Tools[0].InputSchema["properties"].(map[string]any)["name"].(map[string]any)["type"] != "string" {
		t.Error("nested InputSchema was mutated")
	}
	if original.Tools[0].InputSchema["required"].([]string)[0] != "name" {
		t.Error("InputSchema required []string was mutated")
	}
	if original.Tools[0].InputSchema["enum"].([]string)[0] != "hello" {
		t.Error("InputSchema enum []string was mutated")
	}
	if original.Schema["properties"].(map[string]any)["result"].(map[string]any)["type"] != "string" {
		t.Error("nested Schema was mutated")
	}
	if original.ExtraParams["nested"].(map[string]any)["deep"] != "value" {
		t.Error("nested ExtraParams map was mutated")
	}
	if original.ExtraParams["list"].([]any)[1].(map[string]any)["b"] != "c" {
		t.Error("nested ExtraParams slice was mutated")
	}
	if original.Messages[0].ToolCalls[0].Arguments["nested"].(map[string]any)["key"] != "original" {
		t.Error("nested ToolCall Arguments was mutated")
	}
}

// mockHandler is a simple RoundTripper for middleware tests.
type mockHandler struct {
	resp    *model.Response
	lastReq *model.Request
}

func (m *mockHandler) Responses(_ context.Context, req *model.Request) (model.ResponseStream, error) {
	m.lastReq = req
	return nil, nil
}

func TestClone_IsolatesRequest(t *testing.T) {
	inner := &mockHandler{resp: &model.Response{Content: "ok"}}
	clone := NewClone(inner)

	req := &model.Request{
		Model: "test",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: []model.Content{{Type: model.ContentTypeText, Text: "hello"}}},
		},
	}

	_, _ = clone.Responses(context.Background(), req)

	// Mutate what the handler received
	if inner.lastReq != nil && len(inner.lastReq.Messages) > 0 {
		inner.lastReq.Messages[0].Content[0].Text = "mutated"
	}

	// Original should be untouched
	if req.Messages[0].Content[0].Text != "hello" {
		t.Error("Clone did not isolate the request")
	}
}
