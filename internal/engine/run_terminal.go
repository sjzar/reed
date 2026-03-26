package engine

import (
	"errors"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/pkg/jsonutil"
)

// isTerminal returns true when all step runs have reached a terminal status.
func (rs *runState) isTerminal() bool {
	rs.stateMu.RLock()
	defer rs.stateMu.RUnlock()

	for _, sr := range rs.stepRuns {
		if !sr.Status.IsTerminal() {
			return false
		}
	}
	return true
}

// isStuck returns true when no steps are RUNNING but some are still PENDING.
func (rs *runState) isStuck() bool {
	rs.stateMu.RLock()
	defer rs.stateMu.RUnlock()

	hasPending := false
	for _, sr := range rs.stepRuns {
		if sr.Status == model.StepRunning {
			return false
		}
		if sr.Status == model.StepPending {
			hasPending = true
		}
	}
	return hasPending
}

// cancelAllPending marks all PENDING steps as SKIPPED and RUNNING steps as CANCELED.
func (rs *runState) cancelAllPending() {
	type stepEvent struct {
		sr        model.StepRun
		eventType string
	}
	var toEmit []stepEvent

	rs.stateMu.Lock()
	now := rs.eng.now()
	for _, sr := range rs.stepRuns {
		switch sr.Status {
		case model.StepPending:
			sr.Status = model.StepSkipped
			sr.FinishedAt = &now
			toEmit = append(toEmit, stepEvent{sr: *sr, eventType: EventStepFinished})
		case model.StepRunning:
			sr.Status = model.StepCanceled
			sr.FinishedAt = &now
			toEmit = append(toEmit, stepEvent{sr: *sr, eventType: EventStepFailed})
		}
	}
	rs.stateMu.Unlock()

	for i := range toEmit {
		rs.emitStepEvent(&toEmit[i].sr, toEmit[i].eventType)
	}
}

// finalize determines the final Run status based on step outcomes,
// renders outputs, and emits the finalized event.
func (rs *runState) finalize() {
	rs.stateMu.Lock()

	now := rs.eng.now()
	rs.finishedAt = &now

	// If stop was user-triggered, force CANCELED regardless of step outcomes.
	if rs.stopRequested {
		rs.status = model.RunCanceled
		rs.stateMu.Unlock()
		rs.finalizeJobOutputs()
		rs.renderOutputs()
		rs.checkOutputRenderErr()
		rs.emitRunEvent(EventRunFinalized, string(rs.status))
		return
	}

	hasFailed := false
	hasCanceled := false
	for _, sr := range rs.stepRuns {
		switch sr.Status {
		case model.StepFailed:
			hasFailed = true
		case model.StepCanceled:
			hasCanceled = true
		}
	}

	switch {
	case hasFailed:
		rs.status = model.RunFailed
	case hasCanceled:
		rs.status = model.RunCanceled
	default:
		rs.status = model.RunSucceeded
	}

	rs.stateMu.Unlock()

	rs.finalizeJobOutputs()
	rs.renderOutputs()
	rs.checkOutputRenderErr()
	rs.emitRunEvent(EventRunFinalized, string(rs.status))
}

// checkOutputRenderErr checks if output render errors should change run status.
func (rs *runState) checkOutputRenderErr() {
	rs.stateMu.Lock()
	defer rs.stateMu.Unlock()
	if rs.outputRenderErr != nil && rs.status == model.RunSucceeded {
		rs.status = model.RunFailed
		rs.errorCode = "OUTPUT_RENDER_ERROR"
		rs.errorMessage = rs.outputRenderErr.Error()
	}
}

// snapshot builds a read-only RunView from the current state.
// Outputs are shallow-copied at the top level.
func (rs *runState) snapshot() RunView {
	rs.stateMu.RLock()
	defer rs.stateMu.RUnlock()

	view := RunView{
		ID:             rs.id,
		ProcessID:      rs.processID,
		WorkflowSource: rs.request.WorkflowSource,
		Status:         rs.status,
		ErrorCode:      rs.errorCode,
		ErrorMessage:   rs.errorMessage,
		CreatedAt:      rs.createdAt,
		StartedAt:      rs.startedAt,
	}
	if rs.finishedAt != nil {
		t := *rs.finishedAt
		view.FinishedAt = &t
	}
	if rs.request.TriggerMeta != nil {
		view.TriggerMeta = make(map[string]any, len(rs.request.TriggerMeta))
		for k, v := range rs.request.TriggerMeta {
			view.TriggerMeta[k] = jsonutil.DeepClone(v)
		}
	}

	// Rendered workflow outputs
	if rs.renderedOutputs != nil {
		view.Outputs = make(map[string]any, len(rs.renderedOutputs))
		for k, v := range rs.renderedOutputs {
			view.Outputs[k] = jsonutil.DeepClone(v)
		}
	}

	// Build Jobs map from step runs
	view.Jobs = rs.buildJobsView()

	return view
}

// finalizeJobOutputs renders outputs for all succeeded jobs and stores them in renderedJobOutputs.
// Must be called during finalization, before renderOutputs().
func (rs *runState) finalizeJobOutputs() {
	rs.stateMu.Lock()
	rs.renderedJobOutputs = make(map[string]map[string]any, len(rs.jobs))
	var renderErrs []error
	for jobID, job := range rs.jobs {
		if len(job.Outputs) == 0 {
			continue
		}
		jobStepRuns := rs.stepRunsForJob(jobID)
		renderable := true
		for _, sr := range jobStepRuns {
			switch sr.Status {
			case model.StepSucceeded, model.StepSkipped:
				continue
			default:
				renderable = false
			}
		}
		if !renderable {
			continue
		}
		jobSteps := make(map[string]any, len(jobStepRuns))
		for _, sr := range jobStepRuns {
			jobSteps[sr.StepID] = map[string]any{
				"status":  string(sr.Status),
				"outputs": sr.Outputs,
			}
		}
		outputs, err := rs.renderJobOutputs(job, jobSteps)
		if err != nil {
			renderErrs = append(renderErrs, err)
		}
		rs.renderedJobOutputs[jobID] = outputs
	}
	if len(renderErrs) > 0 {
		rs.outputRenderErr = errors.Join(renderErrs...)
	}
	rs.stateMu.Unlock()
}

// buildJobsView constructs the Jobs map for a RunView snapshot.
// Must be called under stateMu.RLock().
func (rs *runState) buildJobsView() map[string]JobView {
	if len(rs.jobs) == 0 {
		return nil
	}

	jobs := make(map[string]JobView, len(rs.jobs))
	for jobID := range rs.jobs {
		jobStepRuns := rs.stepRunsForJob(jobID)
		statuses := make([]model.StepStatus, len(jobStepRuns))
		steps := make(map[string]StepView, len(jobStepRuns))

		for i, sr := range jobStepRuns {
			statuses[i] = sr.Status
			sv := StepView{
				StepID:       sr.StepID,
				StepRunID:    sr.ID,
				Status:       sr.Status,
				IsBackground: sr.Background,
				ErrorCode:    sr.ErrorCode,
				ErrorMessage: sr.ErrorMessage,
			}
			if sr.Outputs != nil {
				sv.Outputs = make(map[string]any, len(sr.Outputs))
				for k, v := range sr.Outputs {
					sv.Outputs[k] = jsonutil.DeepClone(v)
				}
			}
			steps[sr.StepID] = sv
		}

		jv := JobView{
			JobID:  jobID,
			Status: DeriveJobStatus(statuses),
			Steps:  steps,
		}

		if src := rs.renderedJobOutputs[jobID]; src != nil {
			cp := make(map[string]any, len(src))
			for k, v := range src {
				cp[k] = jsonutil.DeepClone(v)
			}
			jv.Outputs = cp
		}

		// Include rendered job outputs if available
		// (from buildRenderContext's job outputs rendering)
		jobs[jobID] = jv
	}
	return jobs
}
