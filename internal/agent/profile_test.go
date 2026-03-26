package agent

import "testing"

func TestResolvedProfile_Defaults(t *testing.T) {
	// Zero-value profile should not interfere with engine defaults
	p := &ResolvedProfile{}

	if p.SystemPrompt != "" {
		t.Error("zero-value SystemPrompt should be empty (engine falls back to DefaultSystemPrompt)")
	}
	if p.ToolIDs != nil {
		t.Error("zero-value ToolIDs should be nil (engine falls back to core tools)")
	}
	if p.SkillIDs != nil {
		t.Error("zero-value SkillIDs should be nil (engine skips skill injection)")
	}
	if p.Temperature != nil {
		t.Error("zero-value Temperature should be nil (engine uses model default)")
	}
}

func TestResolvedProfile_ToolIDsNilVsEmpty(t *testing.T) {
	// nil ToolIDs = "use defaults" vs empty = "no tools" — engine distinguishes these
	nilProfile := &ResolvedProfile{ToolIDs: nil}
	emptyProfile := &ResolvedProfile{ToolIDs: []string{}}

	if nilProfile.ToolIDs != nil {
		t.Error("nil ToolIDs should remain nil")
	}
	if emptyProfile.ToolIDs == nil {
		t.Error("empty ToolIDs should remain non-nil")
	}
	if len(emptyProfile.ToolIDs) != 0 {
		t.Error("empty ToolIDs should have length 0")
	}
}
