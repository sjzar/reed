package engine

import "github.com/sjzar/reed/internal/model"

// DeriveJobStatus infers a job's status from its step statuses.
func DeriveJobStatus(statuses []model.StepStatus) model.RunStatus {
	hasRunning := false
	hasFailed := false
	hasCanceled := false
	hasPending := false
	for _, s := range statuses {
		switch s {
		case model.StepRunning:
			hasRunning = true
		case model.StepFailed:
			hasFailed = true
		case model.StepCanceled:
			hasCanceled = true
		case model.StepPending:
			hasPending = true
		}
	}
	switch {
	case hasFailed:
		return model.RunFailed
	case hasRunning:
		return model.RunRunning
	case hasCanceled:
		return model.RunCanceled
	case hasPending:
		return model.RunStarting
	default:
		return model.RunSucceeded
	}
}
