package worker

import (
	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

// newResult creates a StepRunResult pre-populated with IDs and an empty Outputs map.
func newResult(p engine.StepPayload) engine.StepRunResult {
	return engine.StepRunResult{
		StepRunID: p.StepRunID,
		JobID:     p.JobID,
		StepID:    p.StepID,
		Outputs:   make(map[string]any),
	}
}

// failResult creates a StepRunResult marked as StepFailed with the given error message.
func failResult(p engine.StepPayload, msg string) engine.StepRunResult {
	return engine.StepRunResult{
		StepRunID:    p.StepRunID,
		JobID:        p.JobID,
		StepID:       p.StepID,
		Status:       model.StepFailed,
		ErrorMessage: msg,
	}
}
