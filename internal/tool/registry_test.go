package tool

import (
	"context"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

// stubTool is a minimal Tool implementation for testing.
type stubTool struct {
	name string
}

func (s *stubTool) Def() model.ToolDef {
	return model.ToolDef{Name: s.name, Description: s.name + " desc"}
}
func (s *stubTool) Prepare(_ context.Context, req CallRequest) (*PreparedCall, error) {
	return &PreparedCall{ToolCallID: req.ToolCallID, Name: req.Name, RawArgs: req.RawArgs}, nil
}
func (s *stubTool) Execute(_ context.Context, _ *PreparedCall) (*Result, error) {
	return TextResult("ok"), nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&stubTool{name: "a"}); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.Get("a")
	if !ok {
		t.Fatal("expected to find tool a")
	}
	if tool.Def().Name != "a" {
		t.Fatalf("expected name a, got %s", tool.Def().Name)
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&stubTool{name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&stubTool{name: "a"}); err == nil {
		t.Fatal("expected error on duplicate register")
	}
}

func TestRegistry_GetUnknown(t *testing.T) {
	reg := NewRegistry()
	_, ok := reg.Get("nonexistent")
	if ok {
		t.Fatal("Get returned true for unknown tool")
	}
}

func TestRegistry_ListTools_All(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&stubTool{name: "a"}, &stubTool{name: "b"})
	defs, err := reg.ListTools(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 defs, got %d", len(defs))
	}
}

func TestRegistry_ListTools_Specific(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&stubTool{name: "a"}, &stubTool{name: "b"})
	defs, err := reg.ListTools([]string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Name != "b" {
		t.Fatalf("unexpected defs: %+v", defs)
	}
}

func TestRegistry_ListTools_UnknownID(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&stubTool{name: "a"})
	_, err := reg.ListTools([]string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown tool ID")
	}
}

func TestRegistry_ListToolsLenient(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&stubTool{name: "a"}, &stubTool{name: "b"})
	defs := reg.ListToolsLenient([]string{"a", "nonexistent", "b"})
	if len(defs) != 2 {
		t.Fatalf("expected 2 tools (skipping unknown), got %d", len(defs))
	}
}

func TestRegistry_ListToolsLenient_AllUnknown(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&stubTool{name: "a"})
	defs := reg.ListToolsLenient([]string{"nonexistent"})
	if len(defs) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(defs))
	}
}

func TestRegistry_All(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&stubTool{name: "x"}, &stubTool{name: "y"})
	all := reg.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(all))
	}
}

// --- Tool group tests ---

// mockTool is an alias for stubTool used in group tests.
type mockTool = stubTool

// groupedMockTool implements both Tool and GroupedTool.
type groupedMockTool struct {
	mockTool
	group ToolGroup
}

func (g *groupedMockTool) Group() ToolGroup { return g.group }

func TestRegistryCoreToolIDs(t *testing.T) {
	reg := NewRegistry()
	_ = reg.RegisterWithGroup(&mockTool{name: "read"}, GroupCore)
	_ = reg.RegisterWithGroup(&mockTool{name: "write"}, GroupCore)
	_ = reg.RegisterWithGroup(&mockTool{name: "mcp_search"}, GroupMCP)
	_ = reg.Register(&mockTool{name: "ungrouped"})

	ids := reg.CoreToolIDs()
	if len(ids) != 2 {
		t.Fatalf("CoreToolIDs: got %d, want 2", len(ids))
	}
	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}
	if !idSet["read"] || !idSet["write"] {
		t.Errorf("CoreToolIDs: got %v, want read and write", ids)
	}
}

func TestRegistryDefaultGroupIsOptional(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&mockTool{name: "plain"})

	ids := reg.CoreToolIDs()
	for _, id := range ids {
		if id == "plain" {
			t.Error("ungrouped tool should not be in core")
		}
	}
}

func TestRegistryRegisterWithGroup(t *testing.T) {
	reg := NewRegistry()
	_ = reg.RegisterWithGroup(&mockTool{name: "tool_a"}, GroupMCP)

	// Should not be in core
	ids := reg.CoreToolIDs()
	for _, id := range ids {
		if id == "tool_a" {
			t.Error("MCP tool should not be in core")
		}
	}

	// Should still be gettable
	_, ok := reg.Get("tool_a")
	if !ok {
		t.Error("RegisterWithGroup tool should be retrievable via Get")
	}
}

func TestRegistryGroupedToolInterface(t *testing.T) {
	reg := NewRegistry()
	gt := &groupedMockTool{mockTool: mockTool{name: "auto_core"}, group: GroupCore}
	_ = reg.Register(gt)

	ids := reg.CoreToolIDs()
	found := false
	for _, id := range ids {
		if id == "auto_core" {
			found = true
		}
	}
	if !found {
		t.Error("tool implementing GroupedTool with GroupCore should be in CoreToolIDs")
	}
}

// --- Deterministic ordering tests ---

func TestRegistryCoreToolIDs_Sorted(t *testing.T) {
	reg := NewRegistry()
	_ = reg.RegisterWithGroup(&mockTool{name: "z_tool"}, GroupCore)
	_ = reg.RegisterWithGroup(&mockTool{name: "a_tool"}, GroupCore)
	_ = reg.RegisterWithGroup(&mockTool{name: "m_tool"}, GroupCore)

	ids := reg.CoreToolIDs()
	if len(ids) != 3 {
		t.Fatalf("expected 3 core tools, got %d", len(ids))
	}
	expected := []string{"a_tool", "m_tool", "z_tool"}
	for i, want := range expected {
		if ids[i] != want {
			t.Errorf("CoreToolIDs[%d]: got %q, want %q", i, ids[i], want)
		}
	}
}

func TestRegistryListToolsAll_Sorted(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&mockTool{name: "zeta"}, &mockTool{name: "alpha"}, &mockTool{name: "mid"})

	defs, err := reg.ListTools(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 3 {
		t.Fatalf("expected 3 defs, got %d", len(defs))
	}
	expected := []string{"alpha", "mid", "zeta"}
	for i, want := range expected {
		if defs[i].Name != want {
			t.Errorf("ListTools(nil)[%d]: got %q, want %q", i, defs[i].Name, want)
		}
	}
}

func TestRegistryAll_Sorted(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&mockTool{name: "z"}, &mockTool{name: "a"}, &mockTool{name: "m"})

	all := reg.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(all))
	}
	expected := []string{"a", "m", "z"}
	for i, want := range expected {
		if all[i].Def().Name != want {
			t.Errorf("All()[%d]: got %q, want %q", i, all[i].Def().Name, want)
		}
	}
}
