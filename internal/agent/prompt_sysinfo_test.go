package agent

import (
	"strings"
	"testing"
)

func TestBuildSystemInfo(t *testing.T) {
	tests := []struct {
		name          string
		agentID       string
		modelName     string
		cwd           string
		osName        string
		arch          string
		timezone      string
		contextWindow int
		wantSubs      []string
		wantEmpty     bool
	}{
		{
			name:          "all fields",
			agentID:       "my-agent",
			modelName:     "claude-sonnet-4-20250514",
			cwd:           "/home/user/project",
			osName:        "darwin",
			arch:          "arm64",
			timezone:      "Asia/Shanghai",
			contextWindow: 200000,
			wantSubs: []string{
				"## System Information",
				"- OS: darwin (arm64)",
				"- Working Directory: /home/user/project",
				"- Agent: my-agent",
				"- Model: claude-sonnet-4-20250514",
				"- Timezone: Asia/Shanghai",
				"- Context Window: 200000 tokens",
			},
		},
		{
			name:     "os without arch",
			osName:   "linux",
			wantSubs: []string{"- OS: linux\n"},
		},
		{
			name:     "only cwd",
			cwd:      "/tmp",
			wantSubs: []string{"- Working Directory: /tmp"},
		},
		{
			name:      "empty fields returns empty string",
			wantEmpty: true,
		},
		{
			name:          "zero context window omitted",
			osName:        "darwin",
			arch:          "arm64",
			contextWindow: 0,
			wantSubs:      []string{"- OS: darwin (arm64)"},
		},
		{
			name:     "empty timezone omitted",
			osName:   "darwin",
			arch:     "arm64",
			timezone: "",
		},
		{
			name:     "UTC timezone",
			osName:   "linux",
			timezone: "UTC",
			wantSubs: []string{"- Timezone: UTC"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSystemInfo(tt.agentID, tt.modelName, tt.cwd, tt.osName, tt.arch, tt.timezone, tt.contextWindow)
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("expected empty string, got %q", got)
				}
				return
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("missing %q in:\n%s", sub, got)
				}
			}
			if tt.contextWindow == 0 && strings.Contains(got, "Context Window") {
				t.Error("zero context window should not appear in output")
			}
			if tt.timezone == "" && strings.Contains(got, "Timezone") {
				t.Error("empty timezone should not appear in output")
			}
		})
	}
}
