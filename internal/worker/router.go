package worker

import (
	"context"
	"fmt"

	"github.com/sjzar/reed/internal/engine"
)

// Router dispatches step execution to the appropriate worker based on payload.Uses.
type Router struct {
	workers map[string]engine.Worker
}

// Ensure Router implements engine.Worker.
var _ engine.Worker = (*Router)(nil)

// NewRouter creates a Router with the default set of workers.
func NewRouter() *Router {
	r := &Router{
		workers: make(map[string]engine.Worker),
	}
	shell := &ShellWorker{}
	r.workers["shell"] = shell
	r.workers["bash"] = shell
	r.workers["run"] = shell
	r.workers["http"] = &HTTPWorker{}
	return r
}

// Register adds a worker for the given uses key.
func (r *Router) Register(uses string, w engine.Worker) {
	r.workers[uses] = w
}

// Execute routes the payload to the correct worker by Uses field.
func (r *Router) Execute(ctx context.Context, p engine.StepPayload) engine.StepRunResult {
	w, ok := r.workers[p.Uses]
	if !ok {
		return failResult(p, fmt.Sprintf("unknown uses: %q", p.Uses))
	}
	return w.Execute(ctx, p)
}
