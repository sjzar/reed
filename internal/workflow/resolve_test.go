package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestParseCLIEnvs(t *testing.T) {
	tests := []struct {
		name string
		envs []string
		want map[string]string
	}{
		{"nil", nil, map[string]string{}},
		{"empty", []string{}, map[string]string{}},
		{"single", []string{"FOO=bar"}, map[string]string{"FOO": "bar"}},
		{"multiple", []string{"A=1", "B=2"}, map[string]string{"A": "1", "B": "2"}},
		{"value with equals", []string{"X=a=b"}, map[string]string{"X": "a=b"}},
		{"no equals skipped", []string{"NOPE"}, map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCLIEnvs(tt.envs)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("key %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestResolveRunRequest_Basic(t *testing.T) {
	wf := &model.Workflow{
		Env: map[string]string{"BASE": "val"},
		Jobs: map[string]model.Job{
			"build": {Steps: []model.Step{{ID: "s1"}}},
			"test":  {Needs: []string{"build"}, Steps: []model.Step{{ID: "s2"}}},
		},
	}

	params := model.TriggerParams{
		TriggerType: model.TriggerCLI,
		Env:         map[string]string{"EXTRA": "e"},
	}

	req, err := ResolveRunRequest(context.Background(), wf, "test.yaml", params)
	if err != nil {
		t.Fatal(err)
	}
	if req.WorkflowSource != "test.yaml" {
		t.Errorf("WorkflowSource = %q", req.WorkflowSource)
	}
	if req.TriggerType != model.TriggerCLI {
		t.Errorf("TriggerType = %v", req.TriggerType)
	}
	// Env should merge base + trigger
	if req.Env["BASE"] != "val" || req.Env["EXTRA"] != "e" {
		t.Errorf("Env = %v", req.Env)
	}
	// All jobs should be present (no subgraph filter)
	if len(req.Workflow.Jobs) != 2 {
		t.Errorf("Jobs count = %d, want 2", len(req.Workflow.Jobs))
	}
}

func TestResolveRunRequest_SubgraphFilter(t *testing.T) {
	wf := &model.Workflow{
		Jobs: map[string]model.Job{
			"a": {},
			"b": {Needs: []string{"a"}},
			"c": {},
		},
		RunJobs: []string{"b"},
	}

	req, err := ResolveRunRequest(context.Background(), wf, "f.yaml", model.TriggerParams{
		TriggerType: model.TriggerCLI,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Workflow pointer should be the original (no subgraph clone)
	if req.Workflow != wf {
		t.Error("Workflow pointer should be the original, not a copy")
	}
	// All jobs remain in Workflow.Jobs (filtering deferred to engine)
	if len(req.Workflow.Jobs) != 3 {
		t.Errorf("Jobs count = %d, want 3 (unfiltered)", len(req.Workflow.Jobs))
	}
	// RunJobs should be populated
	if len(req.RunJobs) != 1 || req.RunJobs[0] != "b" {
		t.Errorf("RunJobs = %v, want [b]", req.RunJobs)
	}
}

func TestResolveRunRequest_Inputs(t *testing.T) {
	wf := &model.Workflow{
		Inputs: map[string]model.InputSpec{
			"name":     {Required: true},
			"greeting": {Default: "hello"},
		},
		Jobs: map[string]model.Job{"j": {}},
	}

	// Missing required input
	_, err := ResolveRunRequest(context.Background(), wf, "f.yaml", model.TriggerParams{
		TriggerType: model.TriggerCLI,
	})
	if err == nil {
		t.Fatal("expected error for missing required input")
	}

	// Provided required input
	req, err := ResolveRunRequest(context.Background(), wf, "f.yaml", model.TriggerParams{
		TriggerType: model.TriggerCLI,
		InputValues: map[string]any{"name": "world"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Inputs["name"] != "world" {
		t.Errorf("name = %v", req.Inputs["name"])
	}
	if req.Inputs["greeting"] != "hello" {
		t.Errorf("greeting = %v (should use default)", req.Inputs["greeting"])
	}
}

// --- LoadAndResolve / PrepareWorkflow integration tests ---

func TestLoadAndResolve_NoPatches(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "wf.yaml")
	os.WriteFile(base, []byte("name: test\njobs:\n  j:\n    steps:\n      - uses: shell\n"), 0644)

	wf, err := LoadAndResolve(base, nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadAndResolve: %v", err)
	}
	if wf.Name != "test" {
		t.Errorf("Name = %q, want test", wf.Name)
	}
	if wf.Source != base {
		t.Errorf("Source = %q, want %q", wf.Source, base)
	}
}

func TestLoadAndResolve_WithSetFileAndSet(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "wf.yaml")
	os.WriteFile(base, []byte("name: base\nenv:\n  A: '1'\njobs:\n  j:\n    steps:\n      - uses: shell\n"), 0644)
	patch := filepath.Join(dir, "patch.yaml")
	os.WriteFile(patch, []byte("env:\n  B: '2'\n"), 0644)

	wf, err := LoadAndResolve(base, []string{patch}, []string{"env.C=3"}, nil)
	if err != nil {
		t.Fatalf("LoadAndResolve: %v", err)
	}
	if wf.Env["A"] != "1" {
		t.Errorf("env.A = %q, want 1", wf.Env["A"])
	}
	if wf.Env["B"] != "2" {
		t.Errorf("env.B = %q, want 2", wf.Env["B"])
	}
	if wf.Env["C"] != "3" {
		t.Errorf("env.C = %q, want 3", wf.Env["C"])
	}
}

func TestLoadAndResolve_SetError(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "wf.yaml")
	os.WriteFile(base, []byte("name: test\njobs:\n  j:\n    steps:\n      - uses: shell\n"), 0644)

	_, err := LoadAndResolve(base, nil, []string{"noequals"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--set") {
		t.Errorf("error should contain --set context, got %q", err.Error())
	}
}

func TestPrepareWorkflow_EnvPrecedence(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "wf.yaml")
	os.WriteFile(base, []byte("name: test\njobs:\n  j:\n    steps:\n      - uses: shell\n"), 0644)

	// --set env.FOO=from_set, then --env FOO=from_env; --env should win (appended after)
	wf, err := PrepareWorkflow(base, nil, []string{"env.FOO=from_set"}, nil, []string{"FOO=from_env"})
	if err != nil {
		t.Fatalf("PrepareWorkflow: %v", err)
	}
	if wf.Env["FOO"] != "from_env" {
		t.Errorf("env.FOO = %q, want from_env (--env wins over --set)", wf.Env["FOO"])
	}
}

func TestResolveRunRequest_TriggerOverridesOutputs(t *testing.T) {
	wf := &model.Workflow{
		Outputs: map[string]string{"result": "${{ jobs.j.outputs.out }}"},
		Jobs:    map[string]model.Job{"j": {}},
	}

	triggerOutputs := map[string]string{"custom": "val"}
	req, err := ResolveRunRequest(context.Background(), wf, "f.yaml", model.TriggerParams{
		TriggerType: model.TriggerHTTP,
		Outputs:     triggerOutputs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Outputs["custom"] != "val" {
		t.Errorf("expected trigger outputs override, got %v", req.Outputs)
	}
	if _, ok := req.Outputs["result"]; ok {
		t.Error("workflow-level output should be overridden")
	}
}
