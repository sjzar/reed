package workflow

import (
	"fmt"

	"github.com/sjzar/reed/internal/model"
	"gopkg.in/yaml.v3"
)

// ParseRaw converts a RawWorkflow (map[string]any after merge) into a typed *model.Workflow.
// It uses YAML round-trip to leverage struct tags for deserialization, then applies
// postProcess for defaults and DSL sugar.
func ParseRaw(raw RawWorkflow) (*model.Workflow, error) {
	data, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal raw workflow: %w", err)
	}
	var wf model.Workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("unmarshal workflow: %w", err)
	}
	postProcess(&wf)
	return &wf, nil
}

// postProcess applies defaults and DSL sugar after YAML deserialization:
// 1. Implicit CLI trigger (no on block -> on.cli = {})
// 2. Service default route injection (port but no http/mcp -> POST /)
// 3. Nil map initialization
// 4. Job.ID = map key
// 5. Step ID auto-generation (step_0, step_1, ...)
// 6. Run field promotion to With["run"]
func postProcess(wf *model.Workflow) {
	// 1. Implicit CLI trigger — only when no trigger is set at all
	if wf.On.CLI == nil && wf.On.Service == nil && len(wf.On.Schedule) == 0 {
		wf.On.CLI = &model.CLITrigger{}
	}

	// 2. Service default route: port but no http/mcp -> inject POST /
	if wf.On.Service != nil && len(wf.On.Service.HTTP) == 0 && len(wf.On.Service.MCP) == 0 {
		wf.On.Service.HTTP = []model.HTTPRoute{
			{Path: "/", Method: "POST"},
		}
	}

	// 3. Nil map initialization
	if wf.Inputs == nil {
		wf.Inputs = make(map[string]model.InputSpec)
	}
	if wf.Outputs == nil {
		wf.Outputs = make(map[string]string)
	}
	if wf.Env == nil {
		wf.Env = make(map[string]string)
	}
	if wf.Agents == nil {
		wf.Agents = make(map[string]model.AgentSpec)
	}
	if wf.Skills == nil {
		wf.Skills = make(map[string]model.SkillSpec)
	}
	if wf.MCPServers == nil {
		wf.MCPServers = make(map[string]model.MCPServerSpec)
	}
	if wf.Jobs == nil {
		wf.Jobs = make(map[string]model.Job)
	}
	if wf.Metadata == nil {
		wf.Metadata = make(map[string]any)
	}

	// 4. Job.ID = map key + step processing
	for id, job := range wf.Jobs {
		job.ID = id
		for i := range job.Steps {
			step := &job.Steps[i]
			// 5. Step ID auto-generation
			if step.ID == "" {
				step.ID = fmt.Sprintf("step_%d", i)
			}
			// 6. Run field promotion to With["run"]
			if step.Run != "" {
				if step.With == nil {
					step.With = make(map[string]any)
				}
				if _, exists := step.With["run"]; !exists {
					step.With["run"] = step.Run
				}
				step.Run = "" // clear after promotion
			}
		}
		wf.Jobs[id] = job
	}
}
