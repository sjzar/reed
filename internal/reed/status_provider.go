package reed

import (
	"fmt"
	"time"

	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

// runtimeSnapshotter is the narrow interface for testability.
type runtimeSnapshotter interface {
	Snapshot() engine.ProcessView
	GetRun(runID string) (*engine.RunView, bool)
	StopRun(runID string) bool
}

type statusProvider struct {
	runtime runtimeSnapshotter
}

func newStatusProvider(rt runtimeSnapshotter) *statusProvider {
	return &statusProvider{runtime: rt}
}

func (p *statusProvider) PingData() model.PingResponse {
	snap := p.runtime.Snapshot()
	return model.PingResponse{
		ProcessID: snap.ProcessID,
		PID:       snap.PID,
		Mode:      string(snap.Mode),
		Now:       time.Now().UTC().Format(time.RFC3339),
	}
}

func (p *statusProvider) StatusData() (any, error) {
	snap := p.runtime.Snapshot()
	if snap.ProcessID == "" {
		return nil, fmt.Errorf("process not registered")
	}

	view := model.StatusView{
		ProcessID: snap.ProcessID,
		PID:       snap.PID,
		Mode:      string(snap.Mode),
		Status:    string(snap.Status),
		Uptime:    time.Since(snap.CreatedAt).Truncate(time.Second).String(),
		CreatedAt: snap.CreatedAt,
		Listeners: snap.Listeners,
	}

	for _, rv := range snap.ActiveRuns {
		arv := model.ActiveRunView{
			RunID:          rv.ID,
			WorkflowSource: rv.WorkflowSource,
			Status:         string(rv.Status),
			CreatedAt:      rv.CreatedAt,
			StartedAt:      rv.StartedAt,
			FinishedAt:     rv.FinishedAt,
			Jobs:           buildJobViews(rv.Jobs),
		}
		view.ActiveRuns = append(view.ActiveRuns, arv)
	}

	return view, nil
}

func (p *statusProvider) RunData(runID string) (any, bool) {
	rv, ok := p.runtime.GetRun(runID)
	if !ok {
		return nil, false
	}
	view := model.ActiveRunView{
		RunID:          rv.ID,
		WorkflowSource: rv.WorkflowSource,
		Status:         string(rv.Status),
		CreatedAt:      rv.CreatedAt,
		StartedAt:      rv.StartedAt,
		FinishedAt:     rv.FinishedAt,
		Jobs:           buildJobViews(rv.Jobs),
	}
	return view, true
}

func (p *statusProvider) StopRun(runID string) bool {
	return p.runtime.StopRun(runID)
}

// buildJobViews maps engine.JobView to model.APIJobView.
func buildJobViews(engineJobs map[string]engine.JobView) map[string]model.APIJobView {
	if len(engineJobs) == 0 {
		return nil
	}
	jobs := make(map[string]model.APIJobView, len(engineJobs))
	for jobID, ej := range engineJobs {
		steps := make(map[string]model.APIStepView, len(ej.Steps))
		for stepID, es := range ej.Steps {
			steps[stepID] = model.APIStepView{
				StepID:       es.StepID,
				StepRunID:    es.StepRunID,
				Status:       string(es.Status),
				IsBackground: es.IsBackground,
				Outputs:      es.Outputs,
				ErrorCode:    es.ErrorCode,
				ErrorMessage: es.ErrorMessage,
			}
		}
		jobs[jobID] = model.APIJobView{
			JobID:   ej.JobID,
			Status:  string(ej.Status),
			Outputs: ej.Outputs,
			Steps:   steps,
		}
	}
	return jobs
}
