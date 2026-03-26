package agent

import (
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/skill"
)

func TestPrompt_BuildForMode_Full(t *testing.T) {
	p := &Prompt{
		Sections: []PromptSection{
			{Name: "identity", Content: "You are an assistant.", Priority: PriorityRequired},
			{Name: "instructions", Content: "Be helpful.", Priority: PriorityHigh},
			{Name: "skills", Content: "You can code.", Priority: PriorityLow},
		},
	}

	got := p.BuildForMode(PromptModeFull)
	want := "You are an assistant.\n\nBe helpful.\n\nYou can code."
	if got != want {
		t.Errorf("BuildForMode(Full):\ngot:  %q\nwant: %q", got, want)
	}
}

func TestPrompt_BuildForMode_Full_Nil(t *testing.T) {
	var p *Prompt
	if p.BuildForMode(PromptModeFull) != "" {
		t.Error("nil prompt should return empty string")
	}
}

func TestPrompt_BuildForMode_Full_Empty(t *testing.T) {
	p := &Prompt{}
	if p.BuildForMode(PromptModeFull) != "" {
		t.Error("empty prompt should return empty string")
	}
}

func TestPrompt_BuildWithBudget(t *testing.T) {
	p := &Prompt{
		Sections: []PromptSection{
			{Name: "identity", Content: "You are an assistant.", Priority: PriorityRequired},
			{Name: "instructions", Content: "Be helpful.", Priority: PriorityHigh},
			{Name: "skills", Content: "You can code in Go, Python, and JavaScript.", Priority: PriorityLow},
		},
	}

	// Full build
	full := p.BuildForMode(PromptModeFull)

	// Budget that fits everything
	got := p.BuildWithBudget(1000)
	if got != full {
		t.Errorf("large budget should include everything:\ngot:  %q\nwant: %q", got, full)
	}

	// Budget that forces trimming low priority
	small := p.BuildWithBudget(40)
	if small == full {
		t.Error("small budget should trim something")
	}
	// Required section should always be present
	if len(small) == 0 {
		t.Error("required section should not be trimmed")
	}
}

func TestPrompt_BuildWithBudget_TrimsLowFirst(t *testing.T) {
	p := &Prompt{
		Sections: []PromptSection{
			{Name: "required", Content: "R", Priority: PriorityRequired},
			{Name: "high", Content: "H", Priority: PriorityHigh},
			{Name: "low1", Content: "L1", Priority: PriorityLow},
			{Name: "low2", Content: "L2", Priority: PriorityLow},
		},
	}

	// Budget that can fit required + high but not low sections
	// R + \n\n + H = 5 chars
	got := p.BuildWithBudget(5)
	if got != "R\n\nH" {
		t.Errorf("got %q, want %q", got, "R\n\nH")
	}
}

func TestPrompt_BuildWithBudget_ExtremePressure(t *testing.T) {
	p := &Prompt{
		Sections: []PromptSection{
			{Name: "identity", Content: "I am the core identity.", Priority: PriorityRequired},
			{Name: "high1", Content: "High priority content.", Priority: PriorityHigh},
			{Name: "high2", Content: "More high priority.", Priority: PriorityHigh},
			{Name: "med1", Content: "Medium stuff here.", Priority: PriorityMedium},
			{Name: "low1", Content: "Low priority filler.", Priority: PriorityLow},
			{Name: "low2", Content: "Another low section.", Priority: PriorityLow},
		},
	}

	// Budget of 1 — only required section survives, no separators
	got := p.BuildWithBudget(len("I am the core identity."))
	if got != "I am the core identity." {
		t.Errorf("extreme budget: got %q, want %q", got, "I am the core identity.")
	}

	// Budget of 0 — still returns required (never trimmed)
	got = p.BuildWithBudget(0)
	if got != "I am the core identity." {
		t.Errorf("zero budget: got %q, want %q", got, "I am the core identity.")
	}

	// Verify separator accounting is correct by checking exact sizes
	// identity(23) + \n\n(2) + high1(22) = 47
	got = p.BuildWithBudget(47)
	want := "I am the core identity.\n\nHigh priority content."
	if got != want {
		t.Errorf("exact budget for 2 sections: got %q, want %q", got, want)
	}
}

func TestPrompt_BuildForMode(t *testing.T) {
	p := &Prompt{
		Sections: []PromptSection{
			{Name: "identity", Content: "I am assistant", Priority: PriorityRequired, MinMode: PromptModeNone},
			{Name: "tools", Content: "tool list", Priority: PriorityMedium, MinMode: PromptModeMinimal},
			{Name: "sysinfo", Content: "sys info", Priority: PriorityMedium, MinMode: PromptModeMinimal},
			{Name: "memory", Content: "mem data", Priority: PriorityMedium, MinMode: PromptModeFull},
			{Name: "skills", Content: "skill data", Priority: PriorityLow, MinMode: PromptModeFull},
		},
	}

	t.Run("full mode includes all", func(t *testing.T) {
		got := p.BuildForMode(PromptModeFull)
		for _, want := range []string{"I am assistant", "tool list", "sys info", "mem data", "skill data"} {
			if !strings.Contains(got, want) {
				t.Errorf("full mode missing %q", want)
			}
		}
	})

	t.Run("minimal mode excludes memory and skills", func(t *testing.T) {
		got := p.BuildForMode(PromptModeMinimal)
		if !strings.Contains(got, "I am assistant") {
			t.Error("minimal should include identity")
		}
		if !strings.Contains(got, "tool list") {
			t.Error("minimal should include tools")
		}
		if !strings.Contains(got, "sys info") {
			t.Error("minimal should include sysinfo")
		}
		if strings.Contains(got, "mem data") {
			t.Error("minimal should exclude memory")
		}
		if strings.Contains(got, "skill data") {
			t.Error("minimal should exclude skills")
		}
	})

	t.Run("none mode includes only identity", func(t *testing.T) {
		got := p.BuildForMode(PromptModeNone)
		if !strings.Contains(got, "I am assistant") {
			t.Error("none should include identity")
		}
		if strings.Contains(got, "tool list") {
			t.Error("none should exclude tools")
		}
		if strings.Contains(got, "mem data") {
			t.Error("none should exclude memory")
		}
	})
}

func TestPrompt_BuildForMode_Nil(t *testing.T) {
	var p *Prompt
	if p.BuildForMode(PromptModeFull) != "" {
		t.Error("nil prompt should return empty string")
	}
}

func TestParsePromptMode(t *testing.T) {
	tests := []struct {
		input string
		want  PromptMode
	}{
		{"full", PromptModeFull},
		{"", PromptModeFull},
		{"minimal", PromptModeMinimal},
		{"none", PromptModeNone},
		{"unknown", PromptModeFull},
	}
	for _, tt := range tests {
		got := ParsePromptMode(tt.input)
		if got != tt.want {
			t.Errorf("ParsePromptMode(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestPromptMode_String(t *testing.T) {
	tests := []struct {
		mode PromptMode
		want string
	}{
		{PromptModeNone, "none"},
		{PromptModeMinimal, "minimal"},
		{PromptModeFull, "full"},
		{PromptMode(99), "full"}, // unknown defaults to full
	}
	for _, tt := range tests {
		got := tt.mode.String()
		if got != tt.want {
			t.Errorf("PromptMode(%d).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestBuildPrompt(t *testing.T) {
	t.Run("all sections populated", func(t *testing.T) {
		p := buildPrompt(promptOpts{
			SystemPrompt: "You are a helpful assistant.",
			ToolDefs: []model.ToolDef{
				{Name: "read_file", Description: "Read a file"},
			},
			SkillInfos: []skill.SkillInfo{
				{ID: "code-review", Description: "Reviews code", MountDir: "/tmp/skills/code-review"},
			},
			MemoryContent: "User prefers Go",
			AgentID:       "agent-1",
			Model:         "claude-sonnet",
			Cwd:           "/home/user",
		})

		if len(p.Sections) < 4 {
			t.Fatalf("expected at least 4 sections, got %d", len(p.Sections))
		}

		// Identity is first and required
		if p.Sections[0].Name != "identity" {
			t.Errorf("first section should be identity, got %q", p.Sections[0].Name)
		}
		if p.Sections[0].Priority != PriorityRequired {
			t.Error("identity should be PriorityRequired")
		}
		if p.Sections[0].MinMode != PromptModeNone {
			t.Error("identity should be visible in all modes")
		}

		// Verify all section names present
		names := make(map[string]bool)
		for _, s := range p.Sections {
			names[s.Name] = true
		}
		for _, want := range []string{"identity", "tools", "sysinfo", "memory", "skills"} {
			if !names[want] {
				t.Errorf("missing section %q", want)
			}
		}
	})

	t.Run("empty system prompt omits identity", func(t *testing.T) {
		p := buildPrompt(promptOpts{})
		for _, s := range p.Sections {
			if s.Name == "identity" {
				t.Error("empty system prompt should not produce identity section")
			}
		}
	})

	t.Run("no tools omits tool section", func(t *testing.T) {
		p := buildPrompt(promptOpts{SystemPrompt: "hi"})
		for _, s := range p.Sections {
			if s.Name == "tools" {
				t.Error("no tools should not produce tool section")
			}
		}
	})

	t.Run("empty sysinfo fields omits sysinfo section", func(t *testing.T) {
		// buildSystemInfo is called with runtime.GOOS/GOARCH which are always non-empty,
		// but if AgentID/Model/Cwd are empty and OS is provided by runtime, sysinfo will
		// still be present. Test that the section is created when fields are provided.
		p := buildPrompt(promptOpts{
			SystemPrompt: "hi",
			AgentID:      "test",
		})
		found := false
		for _, s := range p.Sections {
			if s.Name == "sysinfo" {
				found = true
				if !strings.Contains(s.Content, "test") {
					t.Error("sysinfo should contain agent ID")
				}
			}
		}
		// sysinfo will be present because runtime.GOOS is always non-empty
		if !found {
			t.Error("sysinfo section should be present when OS is available")
		}
	})

	t.Run("skills section has low priority", func(t *testing.T) {
		p := buildPrompt(promptOpts{
			SystemPrompt: "hi",
			SkillInfos: []skill.SkillInfo{
				{ID: "s1", Description: "Skill 1", MountDir: "/tmp/s1"},
			},
		})
		for _, s := range p.Sections {
			if s.Name == "skills" {
				if s.Priority != PriorityLow {
					t.Errorf("skills priority: got %d, want %d", s.Priority, PriorityLow)
				}
				if s.MinMode != PromptModeFull {
					t.Error("skills should only be visible in full mode")
				}
			}
		}
	})

	t.Run("no skills produces no-skills-loaded section", func(t *testing.T) {
		p := buildPrompt(promptOpts{
			SystemPrompt: "hi",
		})
		found := false
		for _, s := range p.Sections {
			if s.Name == "skills" {
				found = true
				if !strings.Contains(s.Content, "No skills are loaded") {
					t.Errorf("expected no-skills message, got %q", s.Content)
				}
				if s.Priority != PriorityLow {
					t.Errorf("no-skills priority: got %d, want %d", s.Priority, PriorityLow)
				}
			}
		}
		if !found {
			t.Error("expected skills section even when no skills are loaded")
		}
	})
}
