package reed

import (
	"context"
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/model"
)

func TestBuild_NilWorkflow(t *testing.T) {
	_, err := Build(context.Background(), BuildConfig{})
	if err == nil {
		t.Fatal("expected error for nil workflow")
	}
}

func TestBuild_MinimalWorkflow(t *testing.T) {
	dir := t.TempDir()
	wf := &model.Workflow{
		Source:     dir + "/test.yaml",
		Jobs:       map[string]model.Job{},
		Skills:     map[string]model.SkillSpec{},
		MCPServers: map[string]model.MCPServerSpec{},
	}

	result, err := Build(context.Background(), BuildConfig{
		Workflow:    wf,
		WorkDir:     dir,
		Models:      conf.ModelsConfig{},
		SessionDir:  dir,
		HomeDir:     dir,
		SkillModDir: dir,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer result.Close()

	if result.Router == nil {
		t.Error("Router should not be nil")
	}
	if result.Pool == nil {
		t.Error("Pool should not be nil")
	}
}

func TestBuild_WithAgents(t *testing.T) {
	dir := t.TempDir()
	wf := &model.Workflow{
		Source: dir + "/test.yaml",
		Agents: map[string]model.AgentSpec{
			"coder": {Model: "mock/test", SystemPrompt: "You are a coder."},
		},
		Jobs:       map[string]model.Job{},
		Skills:     map[string]model.SkillSpec{},
		MCPServers: map[string]model.MCPServerSpec{},
	}

	result, err := Build(context.Background(), BuildConfig{
		Workflow:    wf,
		WorkDir:     dir,
		Models:      conf.ModelsConfig{},
		SessionDir:  dir,
		HomeDir:     dir,
		SkillModDir: dir,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer result.Close()

	if result.Router == nil {
		t.Error("Router should not be nil")
	}
}

func TestBuild_InvalidSkillRef(t *testing.T) {
	dir := t.TempDir()
	wf := &model.Workflow{
		Source: dir + "/test.yaml",
		Agents: map[string]model.AgentSpec{
			"coder": {
				Model:  "mock/test",
				Skills: []string{"nonexistent-skill"},
			},
		},
		Jobs:       map[string]model.Job{},
		Skills:     map[string]model.SkillSpec{},
		MCPServers: map[string]model.MCPServerSpec{},
	}

	_, err := Build(context.Background(), BuildConfig{
		Workflow:    wf,
		WorkDir:     dir,
		Models:      conf.ModelsConfig{},
		SessionDir:  dir,
		HomeDir:     dir,
		SkillModDir: dir,
	})
	if err == nil {
		t.Fatal("expected error for unresolvable skill ref")
	}
	if !strings.Contains(err.Error(), "validate skill refs") {
		t.Errorf("error = %q, want to contain 'validate skill refs'", err)
	}
}

func TestBuildResult_Close_NilReceiver(t *testing.T) {
	var r *BuildResult
	r.Close() // should not panic
}

func TestBuildResult_Close_NilPool(t *testing.T) {
	r := &BuildResult{}
	r.Close() // should not panic
}
