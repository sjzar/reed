package workflow

import (
	"fmt"

	"github.com/sjzar/reed/internal/model"
)

// detectCycles checks for circular dependencies in the job DAG.
func detectCycles(jobs map[string]model.Job) error {
	// Build adjacency list
	adj := make(map[string][]string)
	for id, job := range jobs {
		adj[id] = job.Needs
	}

	// DFS-based cycle detection
	const (
		white = 0 // unvisited
		gray  = 1 // in current path
		black = 2 // fully processed
	)
	color := make(map[string]int, len(adj))

	var visit func(node string) error
	visit = func(node string) error {
		color[node] = gray
		for _, dep := range adj[node] {
			switch color[dep] {
			case gray:
				return fmt.Errorf("cycle detected: %s -> %s", node, dep)
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		color[node] = black
		return nil
	}

	for id := range adj {
		if color[id] == white {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// SubgraphJobs returns a filtered jobs map containing only the specified
// root jobs and their transitive dependencies. If roots is empty, returns
// the original jobs map unchanged.
func SubgraphJobs(jobs map[string]model.Job, roots []string) (map[string]model.Job, error) {
	if len(roots) == 0 {
		return jobs, nil
	}

	// Validate roots exist
	for _, r := range roots {
		if _, ok := jobs[r]; !ok {
			return nil, fmt.Errorf("root job %q not found", r)
		}
	}

	// BFS to collect transitive dependencies
	needed := make(map[string]bool)
	queue := make([]string, len(roots))
	copy(queue, roots)

	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if needed[id] {
			continue
		}
		needed[id] = true

		job, ok := jobs[id]
		if !ok {
			continue
		}
		for _, dep := range job.Needs {
			if !needed[dep] {
				queue = append(queue, dep)
			}
		}
	}

	// Build filtered jobs map
	filtered := make(map[string]model.Job, len(needed))
	for id := range needed {
		filtered[id] = jobs[id]
	}
	return filtered, nil
}

// Subgraph returns a filtered *model.Workflow containing only the specified
// root jobs and their transitive dependencies. If roots is empty, returns
// the original workflow unchanged.
func Subgraph(wf *model.Workflow, roots []string) (*model.Workflow, error) {
	if len(roots) == 0 {
		return wf, nil
	}

	filtered, err := SubgraphJobs(wf.Jobs, roots)
	if err != nil {
		return nil, err
	}

	// Clone workflow with filtered jobs
	result := *wf
	result.Jobs = filtered
	return &result, nil
}
