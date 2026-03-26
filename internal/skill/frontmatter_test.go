package skill

import (
	"testing"
)

func TestParseFrontMatter(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantMeta SkillMeta
		wantBody string
		wantErr  bool
	}{
		{
			name: "valid front matter with all fields",
			input: `---
name: my-skill
description: A test skill
license: MIT
compatibility: ">=1.0"
allowed_tools:
  - read_file
  - write_file
metadata:
  author: tester
  version: 1
---
Body content here
`,
			wantMeta: SkillMeta{
				Name:          "my-skill",
				Description:   "A test skill",
				License:       "MIT",
				Compatibility: ">=1.0",
				AllowedTools:  []string{"read_file", "write_file"},
				Metadata:      map[string]any{"author": "tester", "version": 1},
			},
			wantBody: "Body content here\n",
		},
		{
			name:     "no front matter",
			input:    "Just plain body content\nwith multiple lines\n",
			wantMeta: SkillMeta{},
			wantBody: "Just plain body content\nwith multiple lines\n",
		},
		{
			name:     "empty front matter",
			input:    "---\n---\nBody after empty front matter\n",
			wantMeta: SkillMeta{},
			wantBody: "Body after empty front matter\n",
		},
		{
			name:    "unclosed front matter",
			input:   "---\nname: broken\nno closing separator\n",
			wantErr: true,
		},
		{
			name:    "invalid YAML in front matter",
			input:   "---\n: : : not valid yaml\n\t\tbad:\n---\nBody\n",
			wantErr: true,
		},
		{
			name: "front matter with body content after closing",
			input: `---
name: code-review
description: Reviews code
---
# Code Review Skill

This skill reviews code for quality.
`,
			wantMeta: SkillMeta{
				Name:        "code-review",
				Description: "Reviews code",
			},
			wantBody: "# Code Review Skill\n\nThis skill reviews code for quality.\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, body, err := ParseFrontMatter([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if meta.Name != tt.wantMeta.Name {
				t.Errorf("Name = %q, want %q", meta.Name, tt.wantMeta.Name)
			}
			if meta.Description != tt.wantMeta.Description {
				t.Errorf("Description = %q, want %q", meta.Description, tt.wantMeta.Description)
			}
			if meta.License != tt.wantMeta.License {
				t.Errorf("License = %q, want %q", meta.License, tt.wantMeta.License)
			}
			if meta.Compatibility != tt.wantMeta.Compatibility {
				t.Errorf("Compatibility = %q, want %q", meta.Compatibility, tt.wantMeta.Compatibility)
			}
			if len(meta.AllowedTools) != len(tt.wantMeta.AllowedTools) {
				t.Errorf("AllowedTools len = %d, want %d", len(meta.AllowedTools), len(tt.wantMeta.AllowedTools))
			} else {
				for i, tool := range meta.AllowedTools {
					if tool != tt.wantMeta.AllowedTools[i] {
						t.Errorf("AllowedTools[%d] = %q, want %q", i, tool, tt.wantMeta.AllowedTools[i])
					}
				}
			}
			if len(meta.Metadata) != len(tt.wantMeta.Metadata) {
				t.Errorf("Metadata len = %d, want %d", len(meta.Metadata), len(tt.wantMeta.Metadata))
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}
