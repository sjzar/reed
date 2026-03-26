package tool

import "testing"

func TestRegisterBuiltins(t *testing.T) {
	reg := NewRegistry()
	runner := &mockSubAgentRunner{}
	if err := RegisterBuiltins(reg, WithSubAgentRunner(runner)); err != nil {
		t.Fatalf("RegisterBuiltins failed: %v", err)
	}

	expectedTools := []string{
		"bash",
		"read", "write", "edit",
		"ls", "search",
		"spawn_subagent",
	}
	for _, name := range expectedTools {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("tool %q not registered", name)
		}
	}

	all := reg.All()
	if len(all) != len(expectedTools) {
		t.Errorf("expected %d tools, got %d", len(expectedTools), len(all))
	}

	// spawn_subagent must NOT be in core group
	coreIDs := reg.CoreToolIDs()
	for _, id := range coreIDs {
		if id == "spawn_subagent" {
			t.Error("spawn_subagent must not be in core group")
		}
	}
}

func TestRegisterBuiltins_NoRunner(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterBuiltins(reg); err != nil {
		t.Fatalf("RegisterBuiltins with no runner failed: %v", err)
	}

	// All tools should be registered (unconditional)
	expectedTools := []string{
		"bash", "read", "write", "edit",
		"ls", "search", "spawn_subagent",
	}
	for _, name := range expectedTools {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("tool %q not registered", name)
		}
	}
}

func TestRegisterBuiltinsDuplicate(t *testing.T) {
	reg := NewRegistry()
	runner := &mockSubAgentRunner{}
	if err := RegisterBuiltins(reg, WithSubAgentRunner(runner)); err != nil {
		t.Fatalf("first RegisterBuiltins failed: %v", err)
	}
	// second call should fail due to duplicates
	if err := RegisterBuiltins(reg, WithSubAgentRunner(runner)); err == nil {
		t.Fatal("expected error on duplicate registration")
	}
}
