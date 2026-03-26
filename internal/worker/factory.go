package worker

import (
	"github.com/sjzar/reed/internal/agent"
	"github.com/sjzar/reed/internal/model"
)

// Factory holds global resources and creates Worker instances on demand.
type Factory struct {
	workflow    *model.Workflow
	runner      *agent.Runner
	coreToolIDs []string
	skillSvc    workerSkillProvider
}

// NewFactory creates a Factory with the given global resources.
func NewFactory(wf *model.Workflow, runner *agent.Runner, coreToolIDs []string, skillSvc workerSkillProvider) *Factory {
	return &Factory{
		workflow:    wf,
		runner:      runner,
		coreToolIDs: coreToolIDs,
		skillSvc:    skillSvc,
	}
}

// NewRouter creates a Worker Router with all registered worker types.
func (f *Factory) NewRouter() *Router {
	r := NewRouter()
	r.Register("agent", f.newAgentWorker())
	return r
}

// newAgentWorker creates an AgentWorker with injected dependencies.
func (f *Factory) newAgentWorker() *AgentWorker {
	return &AgentWorker{
		workflow:    f.workflow,
		runner:      f.runner,
		coreToolIDs: f.coreToolIDs,
		skillSvc:    f.skillSvc,
	}
}
