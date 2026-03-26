package engine

import (
	"fmt"
	"path/filepath"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/render"
)

// initStepRuns creates PENDING StepRun entries for every step in the workflow.
func (rs *runState) initStepRuns() {
	rs.stateMu.Lock()
	defer rs.stateMu.Unlock()

	for jobID, job := range rs.jobs {
		for i := range job.Steps {
			step := &job.Steps[i]
			stepRunID := rs.eng.newStepRunID()
			sr := &model.StepRun{
				ID:         stepRunID,
				RunID:      rs.id,
				JobID:      jobID,
				StepID:     step.ID,
				Background: step.Background,
				Status:     model.StepPending,
				Outputs:    make(map[string]any),
			}
			rs.stepRuns[stepRunID] = sr
			rs.stepRunIndex[jobID+"\x00"+step.ID] = sr
			rs.stepRunsByJob[jobID] = append(rs.stepRunsByJob[jobID], sr)
		}
	}
}

// dispatchReady finds all steps whose dependencies are satisfied and dispatches them.
// Loops until no more synchronous state changes occur (skip/fail don't go through channel).
func (rs *runState) dispatchReady() {
	for {
		rs.stateMu.RLock()
		ready := rs.findReadyStepRuns()
		rs.stateMu.RUnlock()

		if len(ready) == 0 {
			return
		}

		for _, sr := range ready {
			rs.dispatch(sr)
		}
		// Loop again: dispatch may have sync-skipped/failed steps,
		// which could unblock the next step in the same job.
	}
}

// dispatch evaluates the step, builds a payload, and launches the worker goroutine.
func (rs *runState) dispatch(sr *model.StepRun) {
	payload, ok := rs.buildPayload(sr)
	if !ok {
		return // step was synchronously skipped or failed
	}
	rs.launchWorker(sr, payload)
}

// buildPayload renders expressions, evaluates the `if` condition, and assembles
// a StepPayload. Returns false if the step was synchronously resolved (skip/fail).
func (rs *runState) buildPayload(sr *model.StepRun) (StepPayload, bool) {
	step := rs.findStep(sr.JobID, sr.StepID)
	rctx := rs.buildRenderContext(sr.JobID)

	// Evaluate `if` condition — skip step if falsy.
	// Uses renderStringSafe which treats nil-member-access as nil (matching
	// GitHub Actions behavior where accessing a property of nil returns falsy).
	if step.If != "" {
		val, err := rs.eng.renderStringSafe(step.If, rctx)
		if err != nil {
			rs.failStepRunSync(sr, fmt.Sprintf("render if: %v", err))
			return StepPayload{}, false
		}
		if !render.IsTruthy(val) {
			rs.skipStepRunSync(sr)
			return StepPayload{}, false
		}
	}

	// Render With and Env
	renderedWith, err := rs.renderWith(step.With, rctx)
	if err != nil {
		rs.failStepRunSync(sr, fmt.Sprintf("render with: %v", err))
		return StepPayload{}, false
	}
	renderedEnv, err := rs.renderEnv(step.Env, rctx)
	if err != nil {
		rs.failStepRunSync(sr, fmt.Sprintf("render env: %v", err))
		return StepPayload{}, false
	}

	// Render WorkDir
	workDir := step.WorkDir
	if workDir != "" {
		val, err := rs.eng.renderString(workDir, rctx)
		if err != nil {
			rs.failStepRunSync(sr, fmt.Sprintf("render workdir: %v", err))
			return StepPayload{}, false
		}
		workDir = fmt.Sprintf("%v", val)
	}
	if workDir == "" {
		workDir = rs.request.WorkDir
	}

	// Render agent spec fields if this is an agent step
	var renderedAgentModel, renderedAgentSystemPrompt string
	if agentID, ok := renderedWith["agent"].(string); ok && agentID != "" {
		agentSpec, found := rs.request.Workflow.Agents[agentID]
		if !found {
			rs.failStepRunSync(sr, fmt.Sprintf("agent %q not defined in workflow agents", agentID))
			return StepPayload{}, false
		}
		if agentSpec.Model != "" {
			val, err := rs.eng.renderString(agentSpec.Model, rctx)
			if err != nil {
				rs.failStepRunSync(sr, fmt.Sprintf("render agent model: %v", err))
				return StepPayload{}, false
			}
			renderedAgentModel = fmt.Sprintf("%v", val)
		}
		if agentSpec.SystemPrompt != "" {
			val, err := rs.eng.renderString(agentSpec.SystemPrompt, rctx)
			if err != nil {
				rs.failStepRunSync(sr, fmt.Sprintf("render agent system_prompt: %v", err))
				return StepPayload{}, false
			}
			renderedAgentSystemPrompt = fmt.Sprintf("%v", val)
		}
	}

	// Resolve ToolAccess: workflow-level setting, default to "workdir"
	toolAccess := model.ToolAccessMode(rs.request.Workflow.ToolAccess)
	if toolAccess == "" {
		toolAccess = model.ToolAccessWorkDir
	}

	// Merge env layers: RunRequest.Env (workflow+trigger) → step.Env → REED_RUN_TEMP_DIR
	mergedEnv := make(map[string]string, len(rs.request.Env)+len(renderedEnv)+1)
	for k, v := range rs.request.Env {
		mergedEnv[k] = v
	}
	for k, v := range renderedEnv {
		mergedEnv[k] = v
	}
	if rs.runTempDir != "" {
		mergedEnv["REED_RUN_TEMP_DIR"] = rs.runTempDir
		mergedEnv["REED_RUN_SKILL_DIR"] = filepath.Join(rs.runTempDir, "skills")
	}

	payload := StepPayload{
		StepRunID:                 sr.ID,
		JobID:                     sr.JobID,
		StepID:                    sr.StepID,
		Uses:                      step.Uses,
		With:                      renderedWith,
		Env:                       mergedEnv,
		WorkDir:                   workDir,
		Shell:                     step.Shell,
		Background:                step.Background,
		Timeout:                   step.Timeout,
		ToolAccess:                toolAccess,
		RenderedAgentModel:        renderedAgentModel,
		RenderedAgentSystemPrompt: renderedAgentSystemPrompt,
		Bus:                       rs.eng.bus,
	}
	return payload, true
}

// launchWorker updates step state to RUNNING, emits the lifecycle event,
// and starts the worker goroutine.
func (rs *runState) launchWorker(sr *model.StepRun, payload StepPayload) {
	rs.stateMu.Lock()
	sr.Status = model.StepRunning
	now := rs.eng.now()
	sr.StartedAt = &now
	rs.stateMu.Unlock()

	rs.emitStepEvent(sr, "step_started")

	rs.wg.Add(1)
	go func() {
		defer rs.wg.Done()
		result := rs.eng.worker.Execute(rs.rootCtx, payload)
		select {
		case rs.stepRunResultCh <- result:
		case <-rs.drainCh:
		}
	}()
}

// findReadyStepRuns returns step runs that are PENDING and whose job dependencies are met.
// Must be called under stateMu.RLock().
func (rs *runState) findReadyStepRuns() []*model.StepRun {
	jobDone := rs.completedJobs()

	type jobProgress struct {
		nextIdx int
	}
	progress := make(map[string]*jobProgress)

	for jobID, job := range rs.jobs {
		jp := &jobProgress{nextIdx: -1}
		for i, step := range job.Steps {
			sr := rs.findStepRunByJobAndStep(jobID, step.ID)
			if sr == nil {
				continue
			}
			if sr.Status == model.StepPending || sr.Status == model.StepRunning {
				if jp.nextIdx == -1 {
					jp.nextIdx = i
				}
				break
			}
		}
		progress[jobID] = jp
	}

	var ready []*model.StepRun
	for jobID, jp := range progress {
		if jp.nextIdx == -1 {
			continue
		}
		job := rs.jobs[jobID]
		if !rs.allNeedsMet(job.Needs, jobDone) {
			continue
		}
		step := job.Steps[jp.nextIdx]
		sr := rs.findStepRunByJobAndStep(jobID, step.ID)
		if sr != nil && sr.Status == model.StepPending {
			ready = append(ready, sr)
		}
	}
	return ready
}

// findStep looks up the static Step definition from the Workflow.
func (rs *runState) findStep(jobID, stepID string) *model.Step {
	job := rs.jobs[jobID]
	for i := range job.Steps {
		if job.Steps[i].ID == stepID {
			return &job.Steps[i]
		}
	}
	return nil
}

// findStepRunByJobAndStep looks up a StepRun by job ID and step ID. O(1).
func (rs *runState) findStepRunByJobAndStep(jobID, stepID string) *model.StepRun {
	return rs.stepRunIndex[jobID+"\x00"+stepID]
}

// completedJobs returns a set of job IDs where all steps have succeeded.
func (rs *runState) completedJobs() map[string]bool {
	type counts struct{ total, succeeded int }
	jc := make(map[string]*counts)

	for _, sr := range rs.stepRuns {
		c, ok := jc[sr.JobID]
		if !ok {
			c = &counts{}
			jc[sr.JobID] = c
		}
		c.total++
		if sr.Status == model.StepSucceeded || sr.Status == model.StepSkipped {
			c.succeeded++
		}
	}

	done := make(map[string]bool)
	for jobID, c := range jc {
		if c.total > 0 && c.total == c.succeeded {
			done[jobID] = true
		}
	}
	return done
}

// allNeedsMet checks if all dependency jobs are completed.
func (rs *runState) allNeedsMet(needs []string, jobDone map[string]bool) bool {
	for _, dep := range needs {
		if !jobDone[dep] {
			return false
		}
	}
	return true
}
