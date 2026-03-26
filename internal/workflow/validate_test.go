package workflow

import (
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestValidate_MinimalValid(t *testing.T) {
	wf := &model.Workflow{
		Jobs: map[string]model.Job{
			"build": {
				Steps: []model.Step{
					{ID: "s0", Uses: "shell", With: map[string]any{"run": "echo hello"}},
				},
			},
		},
	}
	if err := Validate(wf); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestValidate_MissingJobs(t *testing.T) {
	wf := &model.Workflow{
		Name: "test",
		Jobs: map[string]model.Job{},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for missing jobs")
	}
	if !strings.Contains(err.Error(), "missing required field: jobs") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_CycleDetection(t *testing.T) {
	wf := &model.Workflow{
		Jobs: map[string]model.Job{
			"a": {
				Needs: []string{"b"},
				Steps: []model.Step{{ID: "s0", Uses: "shell"}},
			},
			"b": {
				Needs: []string{"a"},
				Steps: []model.Step{{ID: "s0", Uses: "shell"}},
			},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected cycle error, got: %v", err)
	}
}

func TestValidate_InvalidNeedsRef(t *testing.T) {
	wf := &model.Workflow{
		Jobs: map[string]model.Job{
			"a": {
				Needs: []string{"nonexistent"},
				Steps: []model.Step{{ID: "s0", Uses: "shell"}},
			},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for invalid needs ref")
	}
	if !strings.Contains(err.Error(), "non-existent job") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_MutuallyExclusiveOnBlock(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			CLI:     &model.CLITrigger{},
			Service: &model.ServiceTrigger{Port: 8080},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for mutually exclusive on block")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ServiceNeedsTrigger(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			Service: &model.ServiceTrigger{},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for service without port")
	}
	if !strings.Contains(err.Error(), "missing or invalid required field: port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_UsesAllowedValues(t *testing.T) {
	for _, uses := range []string{"bash", "agent", "./child.yml"} {
		wf := &model.Workflow{
			Jobs: map[string]model.Job{
				"a": {Steps: []model.Step{{ID: "s0", Uses: uses}}},
			},
		}
		if err := Validate(wf); err != nil {
			t.Errorf("uses=%q should be valid, got: %v", uses, err)
		}
	}
}

func TestValidate_UsesRejectRemoteURL(t *testing.T) {
	wf := &model.Workflow{
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "https://example.com/workflow.yml"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for remote URL in uses")
	}
	if !strings.Contains(err.Error(), "remote uses URL not allowed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_UsesRejectFragment(t *testing.T) {
	wf := &model.Workflow{
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "./x.yml#job_a"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for fragment ref in uses")
	}
	if !strings.Contains(err.Error(), "fragment references not allowed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_UsesRejectRegistryRef(t *testing.T) {
	wf := &model.Workflow{
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "agent/document-expert@v1"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for registry ref in uses")
	}
	if !strings.Contains(err.Error(), "registry references not allowed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_UndefinedAgentRef(t *testing.T) {
	wf := &model.Workflow{
		Agents: map[string]model.AgentSpec{
			"coder": {Model: "claude"},
		},
		Jobs: map[string]model.Job{
			"a": {
				Steps: []model.Step{
					{ID: "s0", Uses: "agent", With: map[string]any{"agent": "nonexistent"}},
				},
			},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for undefined agent ref")
	}
	if !strings.Contains(err.Error(), "undefined agent") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_OutputsAllowJobOutputs(t *testing.T) {
	wf := &model.Workflow{
		Outputs: map[string]string{
			"result": "${{ jobs.build.outputs.artifact }}",
		},
		Jobs: map[string]model.Job{
			"build": {
				Steps: []model.Step{{ID: "s0", Uses: "shell"}},
				Outputs: map[string]string{
					"artifact": "${{ steps.step_0.result }}",
				},
			},
		},
	}
	if err := Validate(wf); err != nil {
		t.Errorf("valid outputs rejected: %v", err)
	}
}

func TestValidate_OutputsRejectStepPiercing(t *testing.T) {
	wf := &model.Workflow{
		Outputs: map[string]string{
			"bad": "${{ jobs.build.steps.step_0.result }}",
		},
		Jobs: map[string]model.Job{
			"build": {
				Steps: []model.Step{{ID: "s0", Uses: "shell"}},
			},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for step piercing in outputs")
	}
	if !strings.Contains(err.Error(), "must not pierce into step internals") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_JobOutputsRejectStepPiercing(t *testing.T) {
	wf := &model.Workflow{
		Jobs: map[string]model.Job{
			"build": {
				Steps: []model.Step{{ID: "s0", Uses: "shell"}},
				Outputs: map[string]string{
					"bad": "${{ jobs.build.steps.step_0.result }}",
				},
			},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for step piercing in job outputs")
	}
	if !strings.Contains(err.Error(), "must not pierce into step internals") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_CLIAndScheduleMutuallyExclusive(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			CLI:      &model.CLITrigger{},
			Schedule: []model.ScheduleRule{{Cron: "0 9 * * 1-5"}},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for cli + schedule")
	}
	if !strings.Contains(err.Error(), "cli and schedule are mutually exclusive") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ServiceAndScheduleCoexist(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			Service:  &model.ServiceTrigger{Port: 8080},
			Schedule: []model.ScheduleRule{{Cron: "0 9 * * 1-5"}},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	if err := Validate(wf); err != nil {
		t.Errorf("service + schedule should be valid, got: %v", err)
	}
}

func TestValidate_ScheduleAtOnLevel(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			Schedule: []model.ScheduleRule{
				{Cron: "0 9 * * 1-5", RunJobs: []string{"report"}},
			},
		},
		Jobs: map[string]model.Job{
			"report": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	if err := Validate(wf); err != nil {
		t.Errorf("valid schedule-only workflow rejected: %v", err)
	}
}

func TestValidate_ScheduleInvalidCron(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			Schedule: []model.ScheduleRule{{Cron: "not-a-cron"}},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for invalid cron")
	}
	if !strings.Contains(err.Error(), "invalid cron expression") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ScheduleBadRunJobs(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			Schedule: []model.ScheduleRule{
				{Cron: "0 9 * * 1-5", RunJobs: []string{"nonexistent"}},
			},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for bad run_jobs in schedule")
	}
	if !strings.Contains(err.Error(), "non-existent job") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_CLICommandsValid(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			CLI: &model.CLITrigger{
				Commands: map[string]model.CLICommand{
					"build": {
						RunJobs: []string{"compile"},
						Inputs:  map[string]model.InputSpec{"target": {Type: "string"}},
					},
				},
			},
		},
		Jobs: map[string]model.Job{
			"compile": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	if err := Validate(wf); err != nil {
		t.Errorf("valid CLI commands rejected: %v", err)
	}
}

func TestValidate_CLICommandsBadRunJobs(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			CLI: &model.CLITrigger{
				Commands: map[string]model.CLICommand{
					"build": {RunJobs: []string{"nonexistent"}},
				},
			},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for bad run_jobs in CLI command")
	}
	if !strings.Contains(err.Error(), "non-existent job") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_TriggerInputsInvalidType(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			CLI: &model.CLITrigger{
				Commands: map[string]model.CLICommand{
					"build": {
						Inputs: map[string]model.InputSpec{"x": {Type: "badtype"}},
					},
				},
			},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for invalid input type")
	}
	if !strings.Contains(err.Error(), "invalid type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_SkillSpecDuplicatePath(t *testing.T) {
	wf := &model.Workflow{
		Skills: map[string]model.SkillSpec{
			"my-skill": {
				Resources: []model.SkillResourceSpec{
					{Path: "SKILL.md", Content: "---\nname: my-skill\ndescription: A\n---\n"},
					{Path: "SKILL.md", Content: "---\nname: my-skill\ndescription: B\n---\n"},
				},
			},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for duplicate SKILL.md path in skill resources")
	}
	if !strings.Contains(err.Error(), "duplicate path") {
		t.Errorf("unexpected error: %v", err)
	}
}
func TestValidate_WorkflowLevelRunJobs(t *testing.T) {
	wf := &model.Workflow{
		RunJobs: []string{"nonexistent"},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for bad workflow-level run_jobs")
	}
	if !strings.Contains(err.Error(), "non-existent job") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ServicePortOnly(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			Service: &model.ServiceTrigger{Port: 8080},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	if err := Validate(wf); err != nil {
		t.Errorf("service with port-only should be valid, got: %v", err)
	}
}

func TestValidate_HTTPRouteInputs(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			Service: &model.ServiceTrigger{
				Port: 8080,
				HTTP: []model.HTTPRoute{
					{
						Path:   "/api/gen",
						Method: "POST",
						Inputs: map[string]model.InputSpec{"q": {Type: "badtype"}},
					},
				},
			},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}
	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for invalid HTTP route input type")
	}
	if !strings.Contains(err.Error(), "invalid type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_HTTPRouteConcurrencyMissingGroup(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			Service: &model.ServiceTrigger{
				Port: 8080,
				HTTP: []model.HTTPRoute{
					{
						Path:   "/api/gen",
						Method: "POST",
						Concurrency: &model.ConcurrencySpec{
							Behavior: model.ConcurrencyBehaviorQueue,
						},
					},
				},
			},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}

	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for missing concurrency group")
	}
	if !strings.Contains(err.Error(), "missing required field: group") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_HTTPRouteConcurrencyUnsupportedBehavior(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			Service: &model.ServiceTrigger{
				Port: 8080,
				HTTP: []model.HTTPRoute{
					{
						Path:   "/api/gen",
						Method: "POST",
						Concurrency: &model.ConcurrencySpec{
							Group:    "${{ inputs.session_id }}",
							Behavior: model.ConcurrencyBehaviorSteer,
						},
					},
				},
			},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}

	err := Validate(wf)
	if err == nil {
		t.Fatal("expected error for unsupported concurrency behavior")
	}
	if !strings.Contains(err.Error(), "unsupported value") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_HTTPRouteConcurrencyQueueValid(t *testing.T) {
	wf := &model.Workflow{
		On: model.OnSpec{
			Service: &model.ServiceTrigger{
				Port: 8080,
				HTTP: []model.HTTPRoute{
					{
						Path:   "/api/gen",
						Method: "POST",
						Concurrency: &model.ConcurrencySpec{
							Group:    "${{ inputs.session_id }}",
							Behavior: model.ConcurrencyBehaviorQueue,
						},
					},
				},
			},
		},
		Jobs: map[string]model.Job{
			"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
		},
	}

	if err := Validate(wf); err != nil {
		t.Fatalf("valid concurrency spec rejected: %v", err)
	}
}

func TestValidate_ToolAccess(t *testing.T) {
	base := func() *model.Workflow {
		return &model.Workflow{
			Jobs: map[string]model.Job{
				"a": {Steps: []model.Step{{ID: "s0", Uses: "shell"}}},
			},
		}
	}

	t.Run("empty is valid (defaults to workdir)", func(t *testing.T) {
		wf := base()
		if err := Validate(wf); err != nil {
			t.Fatalf("expected valid: %v", err)
		}
	})

	t.Run("workdir is valid", func(t *testing.T) {
		wf := base()
		wf.ToolAccess = "workdir"
		if err := Validate(wf); err != nil {
			t.Fatalf("expected valid: %v", err)
		}
	})

	t.Run("full is valid", func(t *testing.T) {
		wf := base()
		wf.ToolAccess = "full"
		if err := Validate(wf); err != nil {
			t.Fatalf("expected valid: %v", err)
		}
	})

	t.Run("invalid value rejected", func(t *testing.T) {
		wf := base()
		wf.ToolAccess = "sandbox"
		err := Validate(wf)
		if err == nil {
			t.Fatal("expected error for invalid tool_access")
		}
		if !strings.Contains(err.Error(), "tool_access") {
			t.Errorf("expected tool_access in error, got: %v", err)
		}
	})
}

func TestValidate_ShellField(t *testing.T) {
	base := func(uses, shell string) *model.Workflow {
		return &model.Workflow{
			Jobs: map[string]model.Job{
				"a": {Steps: []model.Step{{ID: "s0", Uses: uses, Shell: shell}}},
			},
		}
	}

	t.Run("shell on uses:shell is valid", func(t *testing.T) {
		if err := Validate(base("shell", "bash")); err != nil {
			t.Fatalf("expected valid: %v", err)
		}
	})

	t.Run("shell on uses:run is valid", func(t *testing.T) {
		if err := Validate(base("run", "sh")); err != nil {
			t.Fatalf("expected valid: %v", err)
		}
	})

	t.Run("shell on uses:bash with bash is valid", func(t *testing.T) {
		if err := Validate(base("bash", "bash")); err != nil {
			t.Fatalf("expected valid: %v", err)
		}
	})

	t.Run("shell on uses:bash with sh is rejected", func(t *testing.T) {
		err := Validate(base("bash", "sh"))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "does not allow shell") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("shell on uses:agent is rejected", func(t *testing.T) {
		err := Validate(base("agent", "bash"))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("shell on uses:http is rejected", func(t *testing.T) {
		err := Validate(base("http", "bash"))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("unknown shell ID is rejected", func(t *testing.T) {
		err := Validate(base("shell", "fish"))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown shell") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty shell is always valid", func(t *testing.T) {
		if err := Validate(base("shell", "")); err != nil {
			t.Fatalf("expected valid: %v", err)
		}
		if err := Validate(base("agent", "")); err != nil {
			t.Fatalf("expected valid: %v", err)
		}
	})
}
