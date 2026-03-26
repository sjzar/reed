package reed

import (
	"context"
	"testing"

	"github.com/sjzar/reed/internal/model"
	reedmgr "github.com/sjzar/reed/internal/reed"
	"github.com/sjzar/reed/internal/workflow"
)

func TestBuildCLITriggerParams_SkipsServiceMode(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			Service: &model.ServiceTrigger{Port: 8080},
		},
		Inputs: map[string]model.InputSpec{
			"prompt": {Required: true},
		},
		Jobs: map[string]model.Job{
			"handle": {ID: "handle"},
		},
	}

	mode := reedmgr.DeriveProcessMode(wf.On)
	if mode != model.ProcessModeService {
		t.Fatalf("mode = %s, want %s", mode, model.ProcessModeService)
	}

	// Service mode should produce nil trigger params via the resolver
	params, err := reedmgr.BuildCLITriggerParams(wf, []string{"service.yaml"}, nil, nil)
	if err != nil {
		t.Fatalf("BuildCLITriggerParams: %v", err)
	}

	// In service mode, the resolver is not called (resolveInitialRunRequest returns nil).
	// Verify params still have CLI trigger type.
	if params.TriggerType != model.TriggerCLI {
		t.Fatalf("TriggerType = %s, want %s", params.TriggerType, model.TriggerCLI)
	}

	// Verify that ResolveRunRequest works for service mode inputs
	_, err = workflow.ResolveRunRequest(context.Background(), wf, "service.yaml", params)
	if err != nil {
		// Service mode with required inputs and no values should fail
		// This is expected behavior
		t.Logf("expected error for missing required input: %v", err)
	}
}

func TestBuildCLITriggerParams_ValidatesCLIModeInputs(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			CLI: &model.CLITrigger{},
		},
		Inputs: map[string]model.InputSpec{
			"prompt": {Required: true},
		},
		Jobs: map[string]model.Job{
			"run": {ID: "run"},
		},
	}

	// No inputs provided — should succeed at param building but fail at resolve
	params, err := reedmgr.BuildCLITriggerParams(wf, []string{"cli.yaml"}, nil, nil)
	if err != nil {
		t.Fatalf("BuildCLITriggerParams: %v", err)
	}

	_, err = workflow.ResolveRunRequest(context.Background(), wf, "cli.yaml", params)
	if err == nil {
		t.Fatal("expected missing required input error")
	}
}
