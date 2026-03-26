package mcp

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

// mockTransport is a test double for Transport.
type mockTransport struct {
	mu         sync.Mutex
	connected  bool
	tools      []ToolInfo
	callResult *model.ToolResult
	callErr    error
	connectErr error
	pingErr    error
	closed     bool
}

func (m *mockTransport) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.connectErr != nil {
		return m.connectErr
	}
	m.connected = true
	return nil
}

func (m *mockTransport) ListTools(ctx context.Context) ([]ToolInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tools, nil
}

func (m *mockTransport) CallTool(ctx context.Context, name string, args map[string]any) (*model.ToolResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.callErr != nil {
		return nil, m.callErr
	}
	return m.callResult, nil
}

func (m *mockTransport) Ping(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pingErr
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.connected = false
	return nil
}

func mockFactory(transports map[string]*mockTransport) TransportFactory {
	return func(spec model.MCPServerSpec) (Transport, error) {
		t, ok := transports[spec.Command]
		if !ok {
			return nil, fmt.Errorf("unknown transport: %s", spec.Command)
		}
		return t, nil
	}
}

func TestPool_StartAndListTools(t *testing.T) {
	mt := &mockTransport{
		tools: []ToolInfo{
			{Name: "read_file", Description: "Read a file", InputSchema: map[string]any{"type": "object"}},
			{Name: "write_file", Description: "Write a file"},
		},
	}

	pool := NewPool(WithTransportFactory(mockFactory(map[string]*mockTransport{
		"test-server": mt,
	})))

	spec := model.MCPServerSpec{Command: "test-server"}
	err := pool.Start(context.Background(), "server1", spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if pool.ServerStatus("server1") != ServerReady {
		t.Errorf("status: got %v, want %v", pool.ServerStatus("server1"), ServerReady)
	}

	tools, err := pool.ListTools(context.Background(), "server1")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Errorf("tools: got %d, want 2", len(tools))
	}
	if tools[0].Name != "read_file" {
		t.Errorf("tool name: got %q", tools[0].Name)
	}
}

func TestPool_StartAll(t *testing.T) {
	mt1 := &mockTransport{tools: []ToolInfo{{Name: "tool1"}}}
	mt2 := &mockTransport{tools: []ToolInfo{{Name: "tool2"}}}

	pool := NewPool(WithTransportFactory(mockFactory(map[string]*mockTransport{
		"cmd1": mt1,
		"cmd2": mt2,
	})))

	specs := map[string]model.MCPServerSpec{
		"s1": {Command: "cmd1"},
		"s2": {Command: "cmd2"},
	}
	err := pool.StartAll(context.Background(), specs)
	if err != nil {
		t.Fatalf("StartAll: %v", err)
	}

	if pool.ServerStatus("s1") != ServerReady {
		t.Error("s1 should be ready")
	}
	if pool.ServerStatus("s2") != ServerReady {
		t.Error("s2 should be ready")
	}
}

func TestPool_ListAllTools_SingleServer(t *testing.T) {
	mt := &mockTransport{tools: []ToolInfo{{Name: "read"}}}

	pool := NewPool(WithTransportFactory(mockFactory(map[string]*mockTransport{
		"cmd1": mt,
	})))
	pool.Start(context.Background(), "s1", model.MCPServerSpec{Command: "cmd1"})

	tools, err := pool.ListAllTools(context.Background())
	if err != nil {
		t.Fatalf("ListAllTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(tools))
	}
	if tools[0].Name != "s1__read" {
		t.Errorf("single-server tool name: got %q, want %q", tools[0].Name, "s1__read")
	}
}

func TestPool_ListAllTools_Prefixed(t *testing.T) {
	mt1 := &mockTransport{tools: []ToolInfo{{Name: "read"}}}
	mt2 := &mockTransport{tools: []ToolInfo{{Name: "write"}}}

	pool := NewPool(WithTransportFactory(mockFactory(map[string]*mockTransport{
		"cmd1": mt1,
		"cmd2": mt2,
	})))

	pool.Start(context.Background(), "s1", model.MCPServerSpec{Command: "cmd1"})
	pool.Start(context.Background(), "s2", model.MCPServerSpec{Command: "cmd2"})

	tools, err := pool.ListAllTools(context.Background())
	if err != nil {
		t.Fatalf("ListAllTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools: got %d, want 2", len(tools))
	}
	// Multi-server should prefix names
	hasPrefix := false
	for _, tool := range tools {
		if tool.Name == "s1__read" || tool.Name == "s2__write" {
			hasPrefix = true
		}
	}
	if !hasPrefix {
		t.Error("multi-server tools should have prefixed names")
	}
}

func TestPool_CallTool(t *testing.T) {
	mt := &mockTransport{
		tools: []ToolInfo{{Name: "echo"}},
		callResult: &model.ToolResult{
			Content: []model.Content{{Type: "text", Text: "echoed: hello"}},
		},
	}

	pool := NewPool(WithTransportFactory(mockFactory(map[string]*mockTransport{
		"cmd": mt,
	})))
	pool.Start(context.Background(), "s1", model.MCPServerSpec{Command: "cmd"})

	result, err := pool.CallTool(context.Background(), "s1", "echo", map[string]any{"msg": "hello"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content blocks: got %d, want 1", len(result.Content))
	}
	if result.Content[0].Text != "echoed: hello" {
		t.Errorf("text: got %q", result.Content[0].Text)
	}
}

func TestPool_CallTool_ContentProtection(t *testing.T) {
	bigText := make([]byte, 200)
	for i := range bigText {
		bigText[i] = 'x'
	}

	mt := &mockTransport{
		tools: []ToolInfo{{Name: "big"}},
		callResult: &model.ToolResult{
			Content: []model.Content{{Type: "text", Text: string(bigText)}},
		},
	}

	pool := NewPool(
		WithTransportFactory(mockFactory(map[string]*mockTransport{"cmd": mt})),
		WithMaxResultSize(100),
	)
	pool.Start(context.Background(), "s1", model.MCPServerSpec{Command: "cmd"})

	result, err := pool.CallTool(context.Background(), "s1", "big", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// Should have truncated content + warning block
	if len(result.Content) < 2 {
		t.Fatalf("expected truncated content + warning, got %d blocks", len(result.Content))
	}
	// Last block should be the warning
	last := result.Content[len(result.Content)-1]
	if last.Type != "text" || last.Text == "" {
		t.Error("expected warning text block")
	}
}

func TestPool_ConnectFailure(t *testing.T) {
	mt := &mockTransport{
		connectErr: fmt.Errorf("connection refused"),
	}

	pool := NewPool(WithTransportFactory(mockFactory(map[string]*mockTransport{
		"cmd": mt,
	})))

	err := pool.Start(context.Background(), "s1", model.MCPServerSpec{Command: "cmd"})
	if err == nil {
		t.Fatal("expected error")
	}
	if pool.ServerStatus("s1") != ServerFailed {
		t.Errorf("status: got %v, want %v", pool.ServerStatus("s1"), ServerFailed)
	}
}

func TestPool_StopAll(t *testing.T) {
	mt := &mockTransport{tools: []ToolInfo{{Name: "t1"}}}

	pool := NewPool(WithTransportFactory(mockFactory(map[string]*mockTransport{
		"cmd": mt,
	})))
	pool.Start(context.Background(), "s1", model.MCPServerSpec{Command: "cmd"})

	pool.StopAll()

	if pool.ServerStatus("s1") != ServerStopped {
		t.Errorf("status: got %v, want %v", pool.ServerStatus("s1"), ServerStopped)
	}
	if !mt.closed {
		t.Error("transport should be closed")
	}
}

func TestPool_ServerNotFound(t *testing.T) {
	pool := NewPool()

	_, err := pool.ListTools(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent server")
	}

	_, err = pool.CallTool(context.Background(), "nonexistent", "tool", nil)
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestToolResult_ContentSize(t *testing.T) {
	r := &model.ToolResult{
		Content: []model.Content{
			{Text: "hello"},
			{Text: "world"},
		},
	}
	if r.ContentSize() != 10 {
		t.Errorf("size: got %d, want 10", r.ContentSize())
	}
}

func TestToolResult_Truncate(t *testing.T) {
	r := &model.ToolResult{
		Content: []model.Content{
			{Text: "hello"},
			{Text: "world"},
		},
	}
	r.Truncate(7)
	if len(r.Content) != 2 {
		t.Fatalf("blocks: got %d, want 2", len(r.Content))
	}
	if r.Content[1].Text != "wo" {
		t.Errorf("truncated text: got %q, want %q", r.Content[1].Text, "wo")
	}
}

func TestConvertTools_SingleServer(t *testing.T) {
	tools := []ToolInfo{{Name: "read", Description: "Read"}}
	defs := convertTools(tools, "s1", false)
	if defs[0].Name != "read" {
		t.Errorf("name: got %q, want %q", defs[0].Name, "read")
	}
}

func TestConvertTools_MultiServer(t *testing.T) {
	tools := []ToolInfo{{Name: "read", Description: "Read"}}
	defs := convertTools(tools, "s1", true)
	if defs[0].Name != "s1__read" {
		t.Errorf("name: got %q, want %q", defs[0].Name, "s1__read")
	}
}
