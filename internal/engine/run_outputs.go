package engine

import (
	"errors"
	"fmt"

	"github.com/sjzar/reed/internal/model"
)

// renderOutputs renders workflow-level outputs after the run reaches terminal state.
// If render errors occur and the run was SUCCEEDED, it transitions to FAILED.
func (rs *runState) renderOutputs() {
	if rs.request == nil || len(rs.request.Outputs) == 0 {
		return
	}

	// Build render context with all jobs (empty string = all jobs for steps scope)
	// For workflow outputs, we need all jobs' outputs, so we build a full context.
	rctx := rs.buildOutputRenderContext()

	outputs := make(map[string]any, len(rs.request.Outputs))
	var renderErrs []error
	for key, expr := range rs.request.Outputs {
		val, err := rs.eng.renderString(expr, rctx)
		if err != nil {
			renderErrs = append(renderErrs, fmt.Errorf("output %s: %w", key, err))
		} else if val != nil {
			outputs[key] = val
		}
	}

	rs.stateMu.Lock()
	rs.renderedOutputs = outputs
	if len(renderErrs) > 0 {
		rs.outputRenderErr = errors.Join(rs.outputRenderErr, errors.Join(renderErrs...))
	}
	rs.stateMu.Unlock()
}

// buildOutputRenderContext builds a render context for workflow-level output rendering.
// Uses renderedJobOutputs (set during finalization) for consistency with JobView.Outputs.
// Does NOT include a global "steps" key — workflow outputs must reference jobs.<id>.outputs.
func (rs *runState) buildOutputRenderContext() map[string]any {
	wf := rs.request.Workflow
	ctx := map[string]any{
		"env": rs.request.Env,
	}

	inputsCtx := make(map[string]any, len(rs.request.Inputs))
	for k, v := range rs.request.Inputs {
		inputsCtx[k] = v
	}
	ctx["inputs"] = inputsCtx

	secretsCtx := make(map[string]any, len(rs.request.Secrets))
	for k, v := range rs.request.Secrets {
		secretsCtx[k] = v
	}
	ctx["secrets"] = secretsCtx

	rs.stateMu.RLock()
	defer rs.stateMu.RUnlock()

	// jobs: read from renderedJobOutputs for consistency with snapshot JobView.Outputs
	jobs := make(map[string]any, len(wf.Jobs))
	for jobID := range wf.Jobs {
		jobStepRuns := rs.stepRunsForJob(jobID)
		statuses := make([]model.StepStatus, len(jobStepRuns))
		for i, sr := range jobStepRuns {
			statuses[i] = sr.Status
		}
		jobs[jobID] = map[string]any{
			"status":  string(DeriveJobStatus(statuses)),
			"outputs": rs.renderedJobOutputs[jobID],
		}
	}
	ctx["jobs"] = jobs

	return ctx
}
