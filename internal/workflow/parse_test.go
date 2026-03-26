package workflow

import (
	"testing"
)

func TestParse_MinimalWorkflow(t *testing.T) {
	raw := RawWorkflow{
		"name": "test-wf",
		"jobs": map[string]any{
			"build": map[string]any{
				"steps": []any{
					map[string]any{
						"id":   "step1",
						"uses": "shell",
					},
				},
			},
		},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.Name != "test-wf" {
		t.Errorf("Name = %q, want %q", wf.Name, "test-wf")
	}
	if wf.On.CLI == nil {
		t.Error("expected implicit CLI trigger")
	}
	job, ok := wf.Jobs["build"]
	if !ok {
		t.Fatal("missing job 'build'")
	}
	if len(job.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(job.Steps))
	}
	if job.Steps[0].ID != "step1" {
		t.Errorf("step ID = %q, want %q", job.Steps[0].ID, "step1")
	}
}

func TestParse_ServiceTrigger(t *testing.T) {
	raw := RawWorkflow{
		"on": map[string]any{
			"service": map[string]any{
				"port": 8080,
			},
		},
		"jobs": map[string]any{},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.On.CLI != nil {
		t.Error("expected no CLI trigger")
	}
	if wf.On.Service == nil {
		t.Fatal("expected service trigger")
	}
	if wf.On.Service.Port != 8080 {
		t.Errorf("Port = %d, want 8080", wf.On.Service.Port)
	}
	// Default route injection: port-only → POST /
	if len(wf.On.Service.HTTP) != 1 {
		t.Fatalf("expected 1 default HTTP route, got %d", len(wf.On.Service.HTTP))
	}
	if wf.On.Service.HTTP[0].Path != "/" || wf.On.Service.HTTP[0].Method != "POST" {
		t.Errorf("default route = %s %s, want POST /", wf.On.Service.HTTP[0].Method, wf.On.Service.HTTP[0].Path)
	}
}

func TestParse_EnvAndOutputs(t *testing.T) {
	raw := RawWorkflow{
		"env": map[string]any{
			"FOO": "bar",
			"BAZ": "qux",
		},
		"outputs": map[string]any{
			"result": "${{ jobs.build.outputs.artifact }}",
		},
		"jobs": map[string]any{},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want %q", wf.Env["FOO"], "bar")
	}
	if wf.Env["BAZ"] != "qux" {
		t.Errorf("Env[BAZ] = %q, want %q", wf.Env["BAZ"], "qux")
	}
	if wf.Outputs["result"] != "${{ jobs.build.outputs.artifact }}" {
		t.Errorf("Outputs[result] = %q", wf.Outputs["result"])
	}
}

func TestParse_Agents(t *testing.T) {
	raw := RawWorkflow{
		"agents": map[string]any{
			"coder": map[string]any{
				"model":         "claude-sonnet",
				"system_prompt": "You are a coder.",
				"skills":        []any{"code-review", "testing"},
			},
		},
		"jobs": map[string]any{},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	agent, ok := wf.Agents["coder"]
	if !ok {
		t.Fatal("missing agent 'coder'")
	}
	if agent.Model != "claude-sonnet" {
		t.Errorf("Model = %q", agent.Model)
	}
	if agent.SystemPrompt != "You are a coder." {
		t.Errorf("SystemPrompt = %q", agent.SystemPrompt)
	}
	if len(agent.Skills) != 2 || agent.Skills[0] != "code-review" {
		t.Errorf("Skills = %v", agent.Skills)
	}
}

func TestParse_Skills(t *testing.T) {
	raw := RawWorkflow{
		"skills": map[string]any{
			"review": map[string]any{
				"uses": "./skills/review.md",
			},
		},
		"jobs": map[string]any{},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	skill, ok := wf.Skills["review"]
	if !ok {
		t.Fatal("missing skill 'review'")
	}
	if skill.Uses != "./skills/review.md" {
		t.Errorf("Uses = %q", skill.Uses)
	}
}

func TestParse_MCPServers(t *testing.T) {
	raw := RawWorkflow{
		"mcp_servers": map[string]any{
			"fs": map[string]any{
				"transport": "stdio",
				"command":   "npx",
				"args":      []any{"-y", "@anthropic/mcp-fs"},
				"env":       map[string]any{"HOME": "/tmp"},
			},
		},
		"jobs": map[string]any{},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	mcp, ok := wf.MCPServers["fs"]
	if !ok {
		t.Fatal("missing mcp_server 'fs'")
	}
	if mcp.Transport != "stdio" {
		t.Errorf("Transport = %q", mcp.Transport)
	}
	if mcp.Command != "npx" {
		t.Errorf("Command = %q", mcp.Command)
	}
	if len(mcp.Args) != 2 || mcp.Args[0] != "-y" {
		t.Errorf("Args = %v", mcp.Args)
	}
	if mcp.Env["HOME"] != "/tmp" {
		t.Errorf("Env = %v", mcp.Env)
	}
}

func TestParse_Metadata(t *testing.T) {
	raw := RawWorkflow{
		"metadata": map[string]any{
			"team":    "platform",
			"version": 2,
		},
		"jobs": map[string]any{},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.Metadata["team"] != "platform" {
		t.Errorf("Metadata[team] = %v", wf.Metadata["team"])
	}
}

func TestParse_JobWithNeedsAndOutputs(t *testing.T) {
	raw := RawWorkflow{
		"jobs": map[string]any{
			"build": map[string]any{
				"steps": []any{
					map[string]any{"uses": "shell"},
				},
				"outputs": map[string]any{
					"artifact": "${{ steps.step_0.result }}",
				},
			},
			"deploy": map[string]any{
				"needs": []any{"build"},
				"steps": []any{
					map[string]any{"uses": "shell"},
				},
			},
		},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	deploy := wf.Jobs["deploy"]
	if len(deploy.Needs) != 1 || deploy.Needs[0] != "build" {
		t.Errorf("Needs = %v", deploy.Needs)
	}
	build := wf.Jobs["build"]
	if build.Outputs["artifact"] != "${{ steps.step_0.result }}" {
		t.Errorf("Outputs = %v", build.Outputs)
	}
}

func TestParse_StepAdvancedFields(t *testing.T) {
	raw := RawWorkflow{
		"jobs": map[string]any{
			"test": map[string]any{
				"steps": []any{
					map[string]any{
						"id":         "bg-server",
						"uses":       "shell",
						"background": true,
						"timeout":    30,
						"workdir":    "/tmp/work",
						"env":        map[string]any{"PORT": "8080"},
						"if":         "${{ env.CI == 'true' }}",
						"with":       map[string]any{"cmd": "serve"},
					},
				},
			},
		},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	step := wf.Jobs["test"].Steps[0]
	if !step.Background {
		t.Error("expected Background=true")
	}
	if step.Timeout != 30 {
		t.Errorf("Timeout = %v, want 30", step.Timeout)
	}
	if step.WorkDir != "/tmp/work" {
		t.Errorf("WorkDir = %q", step.WorkDir)
	}
	if step.Env["PORT"] != "8080" {
		t.Errorf("Env = %v", step.Env)
	}
	if step.If != "${{ env.CI == 'true' }}" {
		t.Errorf("If = %q", step.If)
	}
	if step.With["cmd"] != "serve" {
		t.Errorf("With = %v", step.With)
	}
}

func TestParse_StepAutoID(t *testing.T) {
	raw := RawWorkflow{
		"jobs": map[string]any{
			"j": map[string]any{
				"steps": []any{
					map[string]any{"uses": "shell"},
					map[string]any{"uses": "shell"},
				},
			},
		},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	steps := wf.Jobs["j"].Steps
	if steps[0].ID != "step_0" {
		t.Errorf("step[0].ID = %q, want step_0", steps[0].ID)
	}
	if steps[1].ID != "step_1" {
		t.Errorf("step[1].ID = %q, want step_1", steps[1].ID)
	}
}

func TestParse_TopLevelFields(t *testing.T) {
	raw := RawWorkflow{
		"app":         "my-app",
		"name":        "wf-name",
		"version":     "1.0.0",
		"description": "A test workflow",
		"jobs":        map[string]any{},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.App != "my-app" {
		t.Errorf("App = %q", wf.App)
	}
	if wf.Version != "1.0.0" {
		t.Errorf("Version = %q", wf.Version)
	}
	if wf.Description != "A test workflow" {
		t.Errorf("Description = %q", wf.Description)
	}
}

func TestParse_ScheduleAtOnLevel(t *testing.T) {
	raw := RawWorkflow{
		"on": map[string]any{
			"schedule": []any{
				map[string]any{"cron": "0 9 * * 1-5", "run_jobs": []any{"report"}},
			},
		},
		"jobs": map[string]any{
			"report": map[string]any{
				"steps": []any{map[string]any{"uses": "shell"}},
			},
		},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.On.CLI != nil {
		t.Error("expected no implicit CLI trigger when schedule is set")
	}
	if len(wf.On.Schedule) != 1 {
		t.Fatalf("Schedule len = %d, want 1", len(wf.On.Schedule))
	}
	if wf.On.Schedule[0].Cron != "0 9 * * 1-5" {
		t.Errorf("Cron = %q", wf.On.Schedule[0].Cron)
	}
	if len(wf.On.Schedule[0].RunJobs) != 1 || wf.On.Schedule[0].RunJobs[0] != "report" {
		t.Errorf("RunJobs = %v", wf.On.Schedule[0].RunJobs)
	}
}

func TestParse_ImplicitCLINotSetWhenSchedule(t *testing.T) {
	raw := RawWorkflow{
		"on": map[string]any{
			"schedule": []any{
				map[string]any{"cron": "0 9 * * *"},
			},
		},
		"jobs": map[string]any{},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.On.CLI != nil {
		t.Error("implicit CLI should not be set when schedule exists")
	}
}

func TestParse_CLICommands(t *testing.T) {
	raw := RawWorkflow{
		"on": map[string]any{
			"cli": map[string]any{
				"commands": map[string]any{
					"build": map[string]any{
						"description": "Build the project",
						"run_jobs":    []any{"compile", "link"},
						"inputs": map[string]any{
							"target": map[string]any{"type": "string", "required": true},
						},
						"outputs": map[string]any{
							"artifact": "${{ jobs.compile.outputs.bin }}",
						},
					},
				},
			},
		},
		"jobs": map[string]any{
			"compile": map[string]any{
				"steps": []any{map[string]any{"uses": "shell"}},
			},
			"link": map[string]any{
				"steps": []any{map[string]any{"uses": "shell"}},
			},
		},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.On.CLI == nil {
		t.Fatal("expected CLI trigger")
	}
	cmd, ok := wf.On.CLI.Commands["build"]
	if !ok {
		t.Fatal("missing command 'build'")
	}
	if cmd.Description != "Build the project" {
		t.Errorf("Description = %q", cmd.Description)
	}
	if len(cmd.RunJobs) != 2 {
		t.Errorf("RunJobs = %v", cmd.RunJobs)
	}
	if len(cmd.Inputs) != 1 {
		t.Errorf("Inputs = %v", cmd.Inputs)
	}
	if cmd.Inputs["target"].Type != "string" {
		t.Errorf("Inputs[target].Type = %q", cmd.Inputs["target"].Type)
	}
	if cmd.Outputs["artifact"] != "${{ jobs.compile.outputs.bin }}" {
		t.Errorf("Outputs = %v", cmd.Outputs)
	}
}

func TestParse_WorkflowLevelRunJobs(t *testing.T) {
	raw := RawWorkflow{
		"run_jobs": []any{"build", "test"},
		"jobs": map[string]any{
			"build": map[string]any{
				"steps": []any{map[string]any{"uses": "shell"}},
			},
			"test": map[string]any{
				"steps": []any{map[string]any{"uses": "shell"}},
			},
		},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(wf.RunJobs) != 2 || wf.RunJobs[0] != "build" || wf.RunJobs[1] != "test" {
		t.Errorf("RunJobs = %v", wf.RunJobs)
	}
}

func TestParse_ServiceWithHTTPRoutes_NoDefaultInjection(t *testing.T) {
	raw := RawWorkflow{
		"on": map[string]any{
			"service": map[string]any{
				"port": 9090,
				"http": []any{
					map[string]any{"path": "/api/gen", "method": "POST"},
				},
			},
		},
		"jobs": map[string]any{},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(wf.On.Service.HTTP) != 1 {
		t.Fatalf("expected 1 HTTP route (no default injection), got %d", len(wf.On.Service.HTTP))
	}
	if wf.On.Service.HTTP[0].Path != "/api/gen" {
		t.Errorf("Path = %q", wf.On.Service.HTTP[0].Path)
	}
}

func TestParse_ServiceWithMCP_NoDefaultInjection(t *testing.T) {
	raw := RawWorkflow{
		"on": map[string]any{
			"service": map[string]any{
				"port": 9090,
				"mcp": []any{
					map[string]any{"name": "gen", "description": "Generate"},
				},
			},
		},
		"jobs": map[string]any{},
	}

	wf, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// MCP is set, so no default HTTP route should be injected
	if len(wf.On.Service.HTTP) != 0 {
		t.Errorf("expected 0 HTTP routes when MCP is set, got %d", len(wf.On.Service.HTTP))
	}
}
