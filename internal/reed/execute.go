package reed

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/security"
	"github.com/sjzar/reed/internal/workflow"
)

// ExecuteOpts holds all parameters for a single workflow execution.
// All fields are pre-resolved by the CLI layer; Execute does not read conf.
type ExecuteOpts struct {
	Workflow       *model.Workflow
	WorkDir        string
	WorkflowSource string
	Mode           model.ProcessMode
	Args           []string // raw CLI args (for subcommand resolution)
	Envs           []string // --env key=value pairs
	Inputs         []string // --input key=value pairs
	SecretSources  []conf.SecretSourceConfig
	OnReady        func(processID string)        // called when runtime is ready (detach readiness)
	SwitchLogger   func(processID string) func() // called after OpenRuntime to switch to per-process logger
	Stdout         io.Writer                     // event consumer output target
}

// ExecuteResult holds the outcome of a workflow execution.
type ExecuteResult struct {
	ProcessID     string
	TerminalRuns  []TerminalRunInfo
	LoggerCleanup func() // per-process logger; CLI layer calls this to release resources
}

// TerminalRunInfo is a summary of a completed run for CLI display.
type TerminalRunInfo struct {
	ID         string
	Status     model.RunStatus
	StartedAt  time.Time
	FinishedAt *time.Time
	Jobs       map[string]TerminalJobInfo
	Outputs    map[string]any
}

// TerminalJobInfo summarizes a completed job.
type TerminalJobInfo struct {
	Steps map[string]TerminalStepInfo
}

// TerminalStepInfo summarizes a completed step.
type TerminalStepInfo struct {
	Status       model.StepStatus
	ErrorMessage string
	Outputs      map[string]any
}

// Execute orchestrates a full workflow execution: secret store, resolver chain,
// manager bootstrap, runtime open/init/start, event streaming, and shutdown.
// It blocks until the workflow completes or ctx is cancelled.
func (m *Manager) Execute(ctx context.Context, opts ExecuteOpts) (*ExecuteResult, error) {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}

	// 1. Build secret store from config sources
	store, err := buildSecretStore(opts.SecretSources)
	if err != nil {
		return nil, err
	}

	// 2. Compose resolver chain
	svc := workflow.NewService()
	resolver := BuildResolver(svc, store)

	// 3. Bootstrap subsystems
	bootstrapOpts := []Option{
		WithDB(),
		WithMedia(),
		WithSecrets(store),
		WithWorkflow(opts.Workflow, opts.WorkDir),
		WithIPC(),
		WithResolver(resolver),
	}
	if opts.Workflow.On.Service != nil {
		bootstrapOpts = append(bootstrapOpts, WithHTTP(opts.Workflow.On.Service.Port))
	}
	if err := m.applyOptions(bootstrapOpts); err != nil {
		return nil, err
	}

	// 4. Resolve initial CLI run request through the middleware chain
	req, err := resolveInitialRunRequest(ctx, m.Resolver(), opts.Workflow, opts.WorkflowSource, opts.Mode, opts.Args, opts.Envs, opts.Inputs)
	if err != nil {
		return nil, err
	}

	if req != nil {
		req.WorkDir = opts.WorkDir

		// Upload media files referenced in CLI inputs
		if uploader := m.MediaUploader(); uploader != nil {
			if err := processMediaInputs(ctx, uploader, opts.Workflow, req); err != nil {
				return nil, err
			}
		}
	}

	// 5. Ensure cleanup on any exit path
	defer m.Shutdown()

	// 6. Register process and open runtime
	runtimeCtx, runtimeCancel := context.WithCancel(ctx)
	defer runtimeCancel()

	processID, err := m.OpenRuntime(runtimeCtx, opts.Mode, opts.WorkflowSource)
	if err != nil {
		return nil, fmt.Errorf("open runtime: %w", err)
	}

	result := &ExecuteResult{ProcessID: processID}

	// 7. Switch to per-process logger (caller-provided)
	if opts.SwitchLogger != nil {
		result.LoggerCleanup = opts.SwitchLogger(processID)
	}

	// 8. Start event consumer BEFORE InitRuntime so we don't miss early lifecycle events
	var consumerDone chan struct{}
	if m.Bus() != nil {
		consumerDone = make(chan struct{})
		go func() {
			defer close(consumerDone)
			RunEventConsumer(runtimeCtx, m.Bus(), opts.Stdout, opts.Mode == model.ProcessModeCLI)
		}()
	}

	// 9. Complete runtime initialization (event log, IPC, run, triggers)
	if err := m.InitRuntime(runtimeCtx, req, opts.Mode); err != nil {
		return result, err
	}

	// 10. Start HTTP listener + scheduler
	if err := m.Start(); err != nil {
		return result, err
	}

	// 11. Signal readiness (detach mode)
	if opts.OnReady != nil {
		opts.OnReady(processID)
	}

	fmt.Fprintf(opts.Stdout, "[%s](PID %d) STARTED\n\n", processID, os.Getpid())
	runErr := m.Run()

	// 12. Shutdown closes the bus, letting the event consumer drain naturally
	m.Shutdown()

	// 13. Wait for consumer to finish draining
	if consumerDone != nil {
		select {
		case <-consumerDone:
		case <-time.After(3 * time.Second):
			runtimeCancel()
			<-consumerDone
		}
	}

	// 14. Collect terminal runs for summary
	for _, rv := range m.Snapshot().TerminalRuns {
		tri := TerminalRunInfo{
			ID:         rv.ID,
			Status:     rv.Status,
			StartedAt:  rv.StartedAt,
			FinishedAt: rv.FinishedAt,
			Outputs:    rv.Outputs,
			Jobs:       make(map[string]TerminalJobInfo),
		}
		for jobID, jv := range rv.Jobs {
			tji := TerminalJobInfo{Steps: make(map[string]TerminalStepInfo)}
			for stepID, sv := range jv.Steps {
				tji.Steps[stepID] = TerminalStepInfo{
					Status:       sv.Status,
					ErrorMessage: sv.ErrorMessage,
					Outputs:      sv.Outputs,
				}
			}
			tri.Jobs[jobID] = tji
		}
		result.TerminalRuns = append(result.TerminalRuns, tri)
	}

	return result, runErr
}

// applyOptions applies additional options to an already-created Manager.
func (m *Manager) applyOptions(opts []Option) error {
	for _, opt := range opts {
		if err := opt(m); err != nil {
			return err
		}
	}
	return nil
}

// buildSecretStore creates a SecretStore from config sources.
// Returns nil store (not error) when no sources are configured.
func buildSecretStore(sources []conf.SecretSourceConfig) (*security.SecretStore, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	ss := make([]security.SecretSource, len(sources))
	for i, s := range sources {
		ss[i] = security.SecretSource{Type: s.Type, Path: s.Path}
	}
	return security.NewSecretStore(ss)
}
