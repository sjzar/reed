package reed

import (
	"context"
	"fmt"
	"strings"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/workflow"
)

// resolveInitialRunRequest builds trigger params from CLI context and resolves
// through the middleware chain. Returns nil for non-CLI modes.
func resolveInitialRunRequest(
	ctx context.Context,
	resolver RunResolver,
	wf *model.Workflow,
	wfSource string,
	mode model.ProcessMode,
	args []string,
	envs []string,
	inputs []string,
) (*model.RunRequest, error) {
	if mode != model.ProcessModeCLI {
		return nil, nil
	}

	triggerParams, err := BuildCLITriggerParams(wf, args, envs, inputs)
	if err != nil {
		return nil, err
	}

	return resolver.ResolveRunRequest(ctx, wf, wfSource, triggerParams)
}

// BuildCLITriggerParams constructs TriggerParams from CLI context,
// resolving subcommand overrides for run_jobs/inputs/outputs.
func BuildCLITriggerParams(wf *model.Workflow, args []string, envs []string, inputs []string) (model.TriggerParams, error) {
	params := model.TriggerParams{
		TriggerType: model.TriggerCLI,
		Env:         workflow.ParseCLIEnvs(envs),
	}
	if len(inputs) > 0 {
		params.InputValues = make(map[string]any, len(inputs))
		for _, kv := range inputs {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return params, fmt.Errorf("invalid --input %q: expected key=value format (e.g. --input prompt='hello')", kv)
			}
			params.InputValues[k] = v
		}
	}
	if wf.On.CLI != nil && len(wf.On.CLI.Commands) > 0 && len(args) > 1 {
		subcmd := args[1]
		cliCmd, ok := wf.On.CLI.Commands[subcmd]
		if !ok {
			names := make([]string, 0, len(wf.On.CLI.Commands))
			for name := range wf.On.CLI.Commands {
				names = append(names, name)
			}
			return params, fmt.Errorf("unknown command %q; available commands: %s. Check the workflow's on.cli.commands section", subcmd, strings.Join(names, ", "))
		}
		params.TriggerMeta = map[string]any{"command": subcmd}
		if len(cliCmd.RunJobs) > 0 {
			params.RunJobs = cliCmd.RunJobs
		}
		if cliCmd.Inputs != nil {
			params.Inputs = cliCmd.Inputs
		}
		if cliCmd.Outputs != nil {
			params.Outputs = cliCmd.Outputs
		}
	}
	return params, nil
}

// DeriveProcessMode determines the ProcessMode from the on block.
func DeriveProcessMode(on model.OnSpec) model.ProcessMode {
	if on.Service != nil {
		return model.ProcessModeService
	}
	if len(on.Schedule) > 0 {
		return model.ProcessModeSchedule
	}
	return model.ProcessModeCLI
}
