package skill

import (
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestValidateSkillName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "my-skill", false},
		{"valid single word", "review", false},
		{"valid multi segment", "code-review-tool", false},
		{"valid with digits", "skill2", false},
		{"invalid uppercase", "MySkill", true},
		{"invalid spaces", "my skill", true},
		{"invalid underscores", "my_skill", true},
		{"invalid empty", "", true},
		{"invalid starts with digit", "2skill", true},
		{"invalid trailing dash", "skill-", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSkillName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSkillName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateMeta(t *testing.T) {
	tests := []struct {
		name    string
		meta    SkillMeta
		wantErr bool
	}{
		{
			name:    "valid meta",
			meta:    SkillMeta{Name: "code-review", Description: "Reviews code"},
			wantErr: false,
		},
		{
			name:    "missing name",
			meta:    SkillMeta{Description: "No name"},
			wantErr: true,
		},
		{
			name:    "missing description",
			meta:    SkillMeta{Name: "code-review"},
			wantErr: true,
		},
		{
			name:    "invalid name format",
			meta:    SkillMeta{Name: "Code_Review", Description: "Bad name"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMeta(tt.meta)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMeta() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateNameMatchesID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		meta    SkillMeta
		wantErr bool
	}{
		{
			name:    "matching",
			id:      "code-review",
			meta:    SkillMeta{Name: "code-review"},
			wantErr: false,
		},
		{
			name:    "mismatching",
			id:      "code-review",
			meta:    SkillMeta{Name: "lint-check"},
			wantErr: true,
		},
		{
			name:    "empty meta name is ok",
			id:      "code-review",
			meta:    SkillMeta{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNameMatchesID(tt.id, tt.meta)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNameMatchesID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestValidateNameMatchesDir(t *testing.T) {
	tests := []struct {
		name    string
		dirName string
		meta    SkillMeta
		wantErr bool
	}{
		{
			name:    "matching",
			dirName: "code-review",
			meta:    SkillMeta{Name: "code-review"},
			wantErr: false,
		},
		{
			name:    "mismatching",
			dirName: "code-review",
			meta:    SkillMeta{Name: "lint-check"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNameMatchesDir(tt.dirName, tt.meta)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNameMatchesDir(%q) error = %v, wantErr %v", tt.dirName, err, tt.wantErr)
			}
		})
	}
}

func TestValidateResources(t *testing.T) {
	tests := []struct {
		name      string
		resources []model.SkillResourceSpec
		wantErr   bool
	}{
		{
			name: "valid path+content with SKILL.md",
			resources: []model.SkillResourceSpec{
				{Path: "SKILL.md", Content: "---\nname: x\ndescription: x\n---\n"},
				{Path: "templates/review.md", Content: "template body"},
			},
			wantErr: false,
		},
		{
			name:      "content only missing path",
			resources: []model.SkillResourceSpec{{Content: "inline content"}},
			wantErr:   true,
		},
		{
			name:      "path only missing content",
			resources: []model.SkillResourceSpec{{Path: "file.md"}},
			wantErr:   true,
		},
		{
			name:      "neither set",
			resources: []model.SkillResourceSpec{{}},
			wantErr:   true,
		},
		{
			name:      "absolute path",
			resources: []model.SkillResourceSpec{{Path: "/etc/secret", Content: "x"}},
			wantErr:   true,
		},
		{
			name: "path with dotdot traversal",
			resources: []model.SkillResourceSpec{
				{Path: "SKILL.md", Content: "---\nname: x\ndescription: x\n---\n"},
				{Path: "../escape/file.md", Content: "x"},
			},
			wantErr: true,
		},
		{
			name: "foo..bar is valid (not traversal)",
			resources: []model.SkillResourceSpec{
				{Path: "SKILL.md", Content: "---\nname: x\ndescription: x\n---\n"},
				{Path: "foo..bar", Content: "x"},
			},
			wantErr: false,
		},
		{
			name: "duplicate explicit path",
			resources: []model.SkillResourceSpec{
				{Path: "SKILL.md", Content: "---\nname: x\ndescription: x\n---\n"},
				{Path: "helper.py", Content: "x"},
				{Path: "helper.py", Content: "y"},
			},
			wantErr: true,
		},
		{
			name: "two resources both resolving to SKILL.md",
			resources: []model.SkillResourceSpec{
				{Path: "SKILL.md", Content: "first"},
				{Path: "SKILL.md", Content: "second"},
			},
			wantErr: true,
		},
		{
			name: "nested SKILL.md rejected",
			resources: []model.SkillResourceSpec{
				{Path: "SKILL.md", Content: "---\nname: x\ndescription: x\n---\n"},
				{Path: "subdir/SKILL.md", Content: "x"},
			},
			wantErr: true,
		},
		{
			name: "no SKILL.md among resources",
			resources: []model.SkillResourceSpec{
				{Path: "helper.py", Content: "x"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResources(tt.resources)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateResources() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSkillSpec(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		spec    model.SkillSpec
		wantErr bool
	}{
		{
			name:    "uses only",
			id:      "review",
			spec:    model.SkillSpec{Uses: "./skills/review"},
			wantErr: false,
		},
		{
			name: "resources only",
			id:   "review",
			spec: model.SkillSpec{
				Resources: []model.SkillResourceSpec{
					{Path: "SKILL.md", Content: "---\nname: review\ndescription: Review skill\n---\nBody\n"},
					{Path: "review.md", Content: "review template"},
				},
			},
			wantErr: false,
		},
		{
			name: "both uses and resources",
			id:   "review",
			spec: model.SkillSpec{
				Uses: "./skills/review",
				Resources: []model.SkillResourceSpec{
					{Path: "SKILL.md", Content: "---\nname: review\ndescription: Review\n---\n"},
				},
			},
			wantErr: true,
		},
		{
			name:    "neither uses nor resources",
			id:      "review",
			spec:    model.SkillSpec{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSkillSpec(tt.id, tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSkillSpec(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}
