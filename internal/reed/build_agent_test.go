package reed

import (
	"context"
	"testing"

	"github.com/sjzar/reed/internal/conf"
)

func TestBuildAgent_Minimal(t *testing.T) {
	dir := t.TempDir()
	result, err := BuildAgent(context.Background(), BuildAgentConfig{
		WorkDir:     dir,
		Models:      conf.ModelsConfig{},
		SessionDir:  dir,
		HomeDir:     dir,
		SkillModDir: dir,
		MemoryDir:   dir,
	})
	if err != nil {
		t.Fatalf("BuildAgent: %v", err)
	}
	defer result.Close()

	if result.Runner == nil {
		t.Error("Runner should not be nil")
	}
	if result.Skills == nil {
		t.Error("Skills should not be nil")
	}
	if len(result.CoreToolIDs) == 0 {
		t.Error("CoreToolIDs should not be empty (builtins are registered)")
	}
}

func TestBuildAgentResult_Close_NilReceiver(t *testing.T) {
	var r *BuildAgentResult
	r.Close() // should not panic
}

func TestBuildAgentResult_Close_Normal(t *testing.T) {
	r := &BuildAgentResult{}
	r.Close() // should not panic
}
