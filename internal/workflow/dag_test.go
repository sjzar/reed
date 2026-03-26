package workflow

import (
	"testing"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/pkg/configpatch"
)

func TestSubgraphJobs_EmptyRoots(t *testing.T) {
	jobs := map[string]model.Job{
		"a": {ID: "a"},
		"b": {ID: "b"},
	}
	result, err := SubgraphJobs(jobs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(result))
	}
}

func TestSubgraphJobs_Filter(t *testing.T) {
	jobs := map[string]model.Job{
		"a": {ID: "a"},
		"b": {ID: "b", Needs: []string{"a"}},
		"c": {ID: "c"},
	}
	result, err := SubgraphJobs(jobs, []string{"b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 jobs (b + dep a), got %d", len(result))
	}
	if _, ok := result["a"]; !ok {
		t.Error("expected job a (transitive dep)")
	}
	if _, ok := result["b"]; !ok {
		t.Error("expected job b (root)")
	}
	if _, ok := result["c"]; ok {
		t.Error("job c should not be in subgraph")
	}
}

func TestSubgraphJobs_NonExistentRoot(t *testing.T) {
	jobs := map[string]model.Job{
		"a": {ID: "a"},
	}
	_, err := SubgraphJobs(jobs, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for non-existent root")
	}
}

func TestSubgraph_EmptyRoots(t *testing.T) {
	wf := &model.Workflow{
		Jobs: map[string]model.Job{
			"a": {ID: "a", Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
			"b": {ID: "b", Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	result, err := Subgraph(wf, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(result.Jobs))
	}
}

func TestSubgraph_SingleRoot(t *testing.T) {
	wf := &model.Workflow{
		Jobs: map[string]model.Job{
			"a": {ID: "a", Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
			"b": {ID: "b", Needs: []string{"a"}, Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
			"c": {ID: "c", Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	result, err := Subgraph(wf, []string{"b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Jobs) != 2 {
		t.Errorf("expected 2 jobs (b + dep a), got %d", len(result.Jobs))
	}
	if _, ok := result.Jobs["a"]; !ok {
		t.Error("expected job a (transitive dep)")
	}
	if _, ok := result.Jobs["b"]; !ok {
		t.Error("expected job b (root)")
	}
	if _, ok := result.Jobs["c"]; ok {
		t.Error("job c should not be in subgraph")
	}
}

func TestSubgraph_NonExistentRoot(t *testing.T) {
	wf := &model.Workflow{
		Jobs: map[string]model.Job{
			"a": {ID: "a", Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	_, err := Subgraph(wf, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for non-existent root")
	}
}

func TestApplySet_Basic(t *testing.T) {
	raw := RawWorkflow{
		"env": map[string]any{"A": "1"},
	}
	result, err := configpatch.ApplySet(raw, []string{"env.A=override"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	env := result["env"].(map[string]any)
	if env["A"] != "override" {
		t.Errorf("env.A = %v, want override", env["A"])
	}
}

func TestApplySet_TypeInference_Nested(t *testing.T) {
	raw := RawWorkflow{
		"config": map[string]any{},
	}
	result, err := configpatch.ApplySet(raw, []string{
		"config.debug=true",
		"config.count=42",
		"config.name=hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := result["config"].(map[string]any)
	if cfg["debug"] != true {
		t.Errorf("debug = %v (%T), want true", cfg["debug"], cfg["debug"])
	}
	if cfg["count"] != 42 {
		t.Errorf("count = %v (%T), want 42", cfg["count"], cfg["count"])
	}
	if cfg["name"] != "hello" {
		t.Errorf("name = %v, want hello", cfg["name"])
	}
}

func TestApplySet_MissingEquals(t *testing.T) {
	raw := RawWorkflow{}
	_, err := configpatch.ApplySet(raw, []string{"noequals"})
	if err == nil {
		t.Fatal("expected error for missing =")
	}
}

func TestApplySet_NullTombstone_Nested(t *testing.T) {
	raw := RawWorkflow{
		"env": map[string]any{"A": "1"},
	}
	result, err := configpatch.ApplySet(raw, []string{"env.A=null"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	env := result["env"].(map[string]any)
	if _, exists := env["A"]; exists {
		t.Errorf("env.A should be deleted by null tombstone, got %v", env["A"])
	}
}
