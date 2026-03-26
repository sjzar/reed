package engine

import (
	"errors"
	"fmt"

	"github.com/sjzar/reed/internal/model"
)

// renderWith renders a step's With map, evaluating all string values as templates.
func (rs *runState) renderWith(w map[string]any, ctx map[string]any) (map[string]any, error) {
	if len(w) == 0 {
		return w, nil
	}
	out := make(map[string]any, len(w))
	for k, v := range w {
		s, ok := v.(string)
		if !ok {
			out[k] = v
			continue
		}
		val, err := rs.eng.renderString(s, ctx)
		if err != nil {
			return nil, fmt.Errorf("render with.%s: %w", k, err)
		}
		out[k] = val
	}
	return out, nil
}

// renderEnv renders a step's Env map, coercing all results to string.
func (rs *runState) renderEnv(env map[string]string, ctx map[string]any) (map[string]string, error) {
	if len(env) == 0 {
		return env, nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		val, err := rs.eng.renderString(v, ctx)
		if err != nil {
			return nil, fmt.Errorf("render env.%s: %w", k, err)
		}
		out[k] = fmt.Sprintf("%v", val)
	}
	return out, nil
}

// buildRenderContext assembles the template context for expression rendering.
// steps scope is per-job (only steps from currentJobID are included).
func (rs *runState) buildRenderContext(currentJobID string) map[string]any {
	wf := rs.request.Workflow
	// Convert env from map[string]string to map[string]any so that
	// missing keys return nil (not ""), enabling ?? nil-coalescing.
	envCtx := make(map[string]any, len(rs.request.Env))
	for k, v := range rs.request.Env {
		envCtx[k] = v
	}
	ctx := map[string]any{
		"env": envCtx,
	}

	// inputs
	inputsCtx := make(map[string]any, len(rs.request.Inputs))
	for k, v := range rs.request.Inputs {
		inputsCtx[k] = v
	}
	ctx["inputs"] = inputsCtx

	// secrets
	secretsCtx := make(map[string]any, len(rs.request.Secrets))
	for k, v := range rs.request.Secrets {
		secretsCtx[k] = v
	}
	ctx["secrets"] = secretsCtx

	// agents
	agents := make(map[string]any, len(wf.Agents))
	for id, a := range wf.Agents {
		agents[id] = map[string]any{
			"model":         a.Model,
			"system_prompt": a.SystemPrompt,
			"skills":        a.Skills,
		}
	}
	ctx["agents"] = agents

	// skills
	skills := make(map[string]any, len(wf.Skills))
	for id, s := range wf.Skills {
		skills[id] = map[string]any{
			"uses": s.Uses,
		}
	}
	ctx["skills"] = skills

	// mcp_servers
	mcpServers := make(map[string]any, len(wf.MCPServers))
	for id, m := range wf.MCPServers {
		mcpServers[id] = map[string]any{
			"transport": m.Transport,
			"command":   m.Command,
			"url":       m.URL,
		}
	}
	ctx["mcp_servers"] = mcpServers

	rs.stateMu.RLock()
	defer rs.stateMu.RUnlock()

	// steps: per-job scope — only steps from currentJobID
	stepsCtx := make(map[string]any)
	for _, sr := range rs.stepRuns {
		if sr.JobID != currentJobID {
			continue
		}
		entry := map[string]any{
			"status":      string(sr.Status),
			"step_run_id": sr.ID,
		}
		if sr.Status == model.StepSucceeded {
			entry["outputs"] = sr.Outputs
		} else {
			entry["outputs"] = map[string]any{}
		}
		stepsCtx[sr.StepID] = entry
	}
	ctx["steps"] = stepsCtx

	// jobs: map[id] → {status, outputs}
	jobs := make(map[string]any)
	for jobID, job := range rs.jobs {
		jobStepRuns := rs.stepRunsForJob(jobID)
		statuses := make([]model.StepStatus, len(jobStepRuns))
		for i, sr := range jobStepRuns {
			statuses[i] = sr.Status
		}
		jobStatus := DeriveJobStatus(statuses)

		// Build job-scoped steps context for rendering job outputs
		jobSteps := make(map[string]any)
		for _, sr := range jobStepRuns {
			stepEntry := map[string]any{
				"status":      string(sr.Status),
				"step_run_id": sr.ID,
			}
			if sr.Status == model.StepSucceeded {
				stepEntry["outputs"] = sr.Outputs
			} else {
				stepEntry["outputs"] = map[string]any{}
			}
			jobSteps[sr.StepID] = stepEntry
		}

		// Render job outputs through explicit mapping
		jobOutputs, _ := rs.renderJobOutputs(job, jobSteps)

		jobs[jobID] = map[string]any{
			"status":  string(jobStatus),
			"outputs": jobOutputs,
		}
	}
	ctx["jobs"] = jobs

	return ctx
}

// renderJobOutputs renders a job's output mapping.
func (rs *runState) renderJobOutputs(job model.Job, jobSteps map[string]any) (map[string]any, error) {
	if len(job.Outputs) == 0 {
		return make(map[string]any), nil
	}
	renderCtx := map[string]any{"steps": jobSteps}
	outputs := make(map[string]any, len(job.Outputs))
	var renderErrs []error
	for key, expr := range job.Outputs {
		val, err := rs.eng.renderString(expr, renderCtx)
		if err != nil {
			renderErrs = append(renderErrs, fmt.Errorf("job output %s: %w", key, err))
		} else {
			outputs[key] = val
		}
	}
	return outputs, errors.Join(renderErrs...)
}

// stepRunsForJob returns all step runs belonging to a given job. O(1).
// Must be called under stateMu.RLock().
func (rs *runState) stepRunsForJob(jobID string) []*model.StepRun {
	return rs.stepRunsByJob[jobID]
}

// failStepRunSync marks a step run as FAILED synchronously (no channel).
// Called from dispatch which runs in the owner loop thread.
func (rs *runState) failStepRunSync(sr *model.StepRun, msg string) {
	rs.stateMu.Lock()
	sr.Status = model.StepFailed
	sr.ErrorMessage = msg
	now := rs.eng.now()
	sr.FinishedAt = &now
	rs.stateMu.Unlock()
	rs.emitStepEvent(sr, EventStepFailed)
}

// skipStepRunSync marks a step run as SKIPPED synchronously (no channel).
// Called from dispatch which runs in the owner loop thread.
func (rs *runState) skipStepRunSync(sr *model.StepRun) {
	rs.stateMu.Lock()
	sr.Status = model.StepSkipped
	now := rs.eng.now()
	sr.FinishedAt = &now
	rs.stateMu.Unlock()
	rs.emitStepEvent(sr, EventStepFinished)
}

// applyStepRunResult applies a worker result to the state tree.
func (rs *runState) applyStepRunResult(res StepRunResult) {
	rs.stateMu.Lock()

	sr, ok := rs.stepRuns[res.StepRunID]
	if !ok {
		rs.stateMu.Unlock()
		return
	}
	sr.Status = res.Status
	sr.Outputs = res.Outputs
	sr.ErrorCode = res.ErrorCode
	sr.ErrorMessage = res.ErrorMessage
	now := rs.eng.now()
	sr.FinishedAt = &now

	eventType := EventStepFinished
	if sr.Status == model.StepFailed {
		eventType = EventStepFailed
	}
	srCopy := *sr
	rs.stateMu.Unlock()

	rs.emitStepEvent(&srCopy, eventType)
}
