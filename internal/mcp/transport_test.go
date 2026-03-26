package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sjzar/reed/internal/model"
)

// --- Factory validation tests ---

func TestNewTransportFactory_StdioMissingCommand(t *testing.T) {
	factory := NewTransportFactory()
	_, err := factory(model.MCPServerSpec{Transport: "stdio"})
	if err == nil {
		t.Fatal("expected error for stdio without command")
	}
}

func TestNewTransportFactory_DefaultStdioMissingCommand(t *testing.T) {
	factory := NewTransportFactory()
	_, err := factory(model.MCPServerSpec{})
	if err == nil {
		t.Fatal("expected error for default (stdio) without command")
	}
}

func TestNewTransportFactory_SSEMissingURL(t *testing.T) {
	factory := NewTransportFactory()
	_, err := factory(model.MCPServerSpec{Transport: "sse"})
	if err == nil {
		t.Fatal("expected error for sse without url")
	}
}

func TestNewTransportFactory_StreamableHTTPMissingURL(t *testing.T) {
	factory := NewTransportFactory()
	_, err := factory(model.MCPServerSpec{Transport: "streamable-http"})
	if err == nil {
		t.Fatal("expected error for streamable-http without url")
	}
}

func TestNewTransportFactory_UnsupportedTransport(t *testing.T) {
	factory := NewTransportFactory()
	_, err := factory(model.MCPServerSpec{Transport: "grpc"})
	if err == nil {
		t.Fatal("expected error for unsupported transport")
	}
}

func TestNewTransportFactory_ValidStdio(t *testing.T) {
	factory := NewTransportFactory()
	tr, err := factory(model.MCPServerSpec{Command: "echo", Args: []string{"hello"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
}

func TestNewTransportFactory_ValidSSE(t *testing.T) {
	factory := NewTransportFactory()
	tr, err := factory(model.MCPServerSpec{Transport: "sse", URL: "http://localhost:8080"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
}

func TestNewTransportFactory_ValidStreamableHTTP(t *testing.T) {
	factory := NewTransportFactory()
	tr, err := factory(model.MCPServerSpec{Transport: "streamable-http", URL: "http://localhost:8080"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
}

// --- Error classification tests ---

func TestClassifyError_Nil(t *testing.T) {
	if classifyError(nil) != nil {
		t.Error("expected nil for nil error")
	}
}

func TestClassifyError_Table(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind ConnErrorKind
	}{
		{"connection closed", gosdk.ErrConnectionClosed, ConnErrOffline},
		{"exit status", errors.New("process exit status 1"), ConnErrStdioExit},
		{"broken pipe", errors.New("write: broken pipe"), ConnErrOffline},
		{"connection refused", errors.New("dial: connection refused"), ConnErrOffline},
		{"connection reset", errors.New("read: connection reset by peer"), ConnErrOffline},
		{"EOF", errors.New("unexpected EOF"), ConnErrOffline},
		{"401 unauthorized", errors.New("failed to connect: 401 Unauthorized"), ConnErrAuth},
		{"403 forbidden", errors.New("failed to connect: 403 Forbidden"), ConnErrAuth},
		{"generic error", errors.New("something went wrong"), ConnErrOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyError(tt.err)
			var ce *ConnError
			if !errors.As(err, &ce) {
				t.Fatalf("expected *ConnError, got %T", err)
			}
			if ce.Kind != tt.kind {
				t.Errorf("kind: got %q, want %q", ce.Kind, tt.kind)
			}
			if ce.Cause != tt.err {
				t.Error("cause should be the original error")
			}
		})
	}
}

// --- Conversion tests ---

func TestConvertSDKResult_TextContent(t *testing.T) {
	r := &gosdk.CallToolResult{
		Content: []gosdk.Content{
			&gosdk.TextContent{Text: "hello"},
		},
	}
	result := convertSDKResult(r)
	if len(result.Content) != 1 {
		t.Fatalf("blocks: got %d, want 1", len(result.Content))
	}
	if result.Content[0].Type != "text" || result.Content[0].Text != "hello" {
		t.Errorf("got %+v", result.Content[0])
	}
	if result.IsError {
		t.Error("expected IsError=false")
	}
}

func TestConvertSDKResult_ImageContent(t *testing.T) {
	r := &gosdk.CallToolResult{
		Content: []gosdk.Content{
			&gosdk.ImageContent{Data: []byte("base64data"), MIMEType: "image/png"},
		},
	}
	result := convertSDKResult(r)
	if len(result.Content) != 1 {
		t.Fatalf("blocks: got %d, want 1", len(result.Content))
	}
	if result.Content[0].Type != "image" {
		t.Errorf("type: got %q, want %q", result.Content[0].Type, "image")
	}
}

func TestConvertSDKResult_IsError(t *testing.T) {
	r := &gosdk.CallToolResult{
		Content: []gosdk.Content{
			&gosdk.TextContent{Text: "tool failed"},
		},
		IsError: true,
	}
	result := convertSDKResult(r)
	if !result.IsError {
		t.Error("expected IsError=true")
	}
}

func TestConvertSDKResult_EmptyContent(t *testing.T) {
	r := &gosdk.CallToolResult{}
	result := convertSDKResult(r)
	if len(result.Content) != 0 {
		t.Errorf("blocks: got %d, want 0", len(result.Content))
	}
}

func TestConvertSDKTool_WithSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
	}
	// JSON round-trip to simulate what the SDK returns
	data, _ := json.Marshal(schema)
	var inputSchema any
	json.Unmarshal(data, &inputSchema)

	tool := &gosdk.Tool{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: inputSchema,
	}
	info := convertSDKTool(tool)
	if info.Name != "read_file" {
		t.Errorf("name: got %q", info.Name)
	}
	if info.InputSchema == nil {
		t.Fatal("expected non-nil InputSchema")
	}
	if info.InputSchema["type"] != "object" {
		t.Errorf("schema type: got %v", info.InputSchema["type"])
	}
}

func TestConvertSDKTool_NilSchema(t *testing.T) {
	tool := &gosdk.Tool{Name: "ping", Description: "Ping"}
	info := convertSDKTool(tool)
	if info.InputSchema != nil {
		t.Error("expected nil InputSchema")
	}
}

// --- buildEnv tests ---

func TestBuildEnv_Nil(t *testing.T) {
	result := buildEnv(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestBuildEnv_Empty(t *testing.T) {
	result := buildEnv(map[string]string{})
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestBuildEnv_Overlay(t *testing.T) {
	result := buildEnv(map[string]string{"FOO": "bar"})
	if result == nil {
		t.Fatal("expected non-nil")
	}
	// Should contain parent env + overlay
	if len(result) <= len(os.Environ()) {
		t.Error("expected overlay to be appended")
	}
	found := false
	for _, e := range result {
		if e == "FOO=bar" {
			found = true
		}
	}
	if !found {
		t.Error("overlay FOO=bar not found")
	}
}

// --- Integration test using in-memory transports ---

func TestSDKTransport_Integration(t *testing.T) {
	// Create in-memory transport pair
	serverTransport, clientTransport := gosdk.NewInMemoryTransports()

	// Create and configure server
	server := gosdk.NewServer(&gosdk.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	// Add a tool to the server
	gosdk.AddTool(server, &gosdk.Tool{
		Name:        "echo",
		Description: "Echo the input",
	}, func(ctx context.Context, req *gosdk.CallToolRequest, input struct {
		Message string `json:"message"`
	}) (*gosdk.CallToolResult, any, error) {
		return &gosdk.CallToolResult{
			Content: []gosdk.Content{
				&gosdk.TextContent{Text: "echo: " + input.Message},
			},
		}, nil, nil
	})

	// Connect server (must happen before client)
	ctx := context.Background()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()

	// Create our sdkTransport wrapping the client side
	client := gosdk.NewClient(&gosdk.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	st := &sdkTransport{
		client:      client,
		mkTransport: func() gosdk.Transport { return clientTransport },
	}

	// Test Connect
	if err := st.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer st.Close()

	// Test ListTools
	tools, err := st.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Errorf("tool name: got %q, want %q", tools[0].Name, "echo")
	}
	if tools[0].Description != "Echo the input" {
		t.Errorf("tool description: got %q", tools[0].Description)
	}

	// Test CallTool
	result, err := st.CallTool(ctx, "echo", map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content blocks: got %d, want 1", len(result.Content))
	}
	if result.Content[0].Text != "echo: hello" {
		t.Errorf("text: got %q, want %q", result.Content[0].Text, "echo: hello")
	}
	if result.IsError {
		t.Error("expected IsError=false")
	}

	// Test Ping
	if err := st.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
