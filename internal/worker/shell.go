package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

// shellInvocation holds the resolved shell binary path and its invocation flags.
type shellInvocation struct {
	path string
	args []string // flags before the command string, e.g. ["-c"]
}

// resolveShellInvocation resolves the shell to use based on the step's uses field
// and optional shell override. Delegates to platform-specific resolveShellPlatform.
func resolveShellInvocation(uses, requested string) (shellInvocation, error) {
	// uses: bash rejects shell override (unless empty or "bash")
	if uses == "bash" && requested != "" && requested != "bash" {
		return shellInvocation{}, fmt.Errorf("uses: bash does not allow shell override %q", requested)
	}
	return resolveShellPlatform(uses, requested)
}

// ShellWorker executes steps by running shell commands.
// It is the default worker for steps with uses: "shell", "bash", or "run".
type ShellWorker struct{}

// Ensure ShellWorker implements engine.Worker.
var _ engine.Worker = (*ShellWorker)(nil)

func (w *ShellWorker) Execute(ctx context.Context, p engine.StepPayload) engine.StepRunResult {
	result := newResult(p)

	cmdStr, _ := p.With["run"].(string)
	if cmdStr == "" {
		cmdStr, _ = p.With["cmd"].(string)
	}
	if cmdStr == "" {
		result.Status = model.StepFailed
		result.ErrorMessage = "shell worker: no 'run' or 'cmd' in with"
		return result
	}

	// Resolve shell invocation
	inv, err := resolveShellInvocation(p.Uses, p.Shell)
	if err != nil {
		result.Status = model.StepFailed
		result.ErrorMessage = "shell worker: " + err.Error()
		return result
	}

	// Apply timeout if specified
	var cancel context.CancelFunc
	if p.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.Timeout)*time.Second)
		defer cancel()
	}

	cmd := exec.Command(inv.path, append(inv.args, cmdStr)...)
	setSysProcAttr(cmd)

	// WorkDir: resolve relative paths and symlinks
	if p.WorkDir != "" {
		dir, err := resolveWorkDir(p.WorkDir, "shell worker")
		if err != nil {
			result.Status = model.StepFailed
			result.ErrorMessage = err.Error()
			return result
		}
		cmd.Dir = dir
	}

	// Environment: inherit OS env, then overlay step-level env
	env := os.Environ()
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	stdoutW := newLimitedWriter(maxOutputBytes)
	stderrW := newLimitedWriter(maxOutputBytes)

	// Stream output via the event bus.
	emitter := newStepEmitter(p)
	cmd.Stdout = emitter.OutputWriter(stdoutW)
	cmd.Stderr = emitter.OutputWriter(stderrW)

	emitter.PublishStatusf("shell_start cmd=%s", inv.path)

	start := time.Now()
	err = startAndWait(ctx, cmd)
	elapsed := time.Since(start)

	if err != nil {
		emitter.PublishStatusf("shell_end status=failed duration=%s", elapsed)
	} else {
		emitter.PublishStatusf("shell_end status=succeeded duration=%s", elapsed)
	}

	result.Outputs["stdout"] = stdoutW.String()
	result.Outputs["stderr"] = stderrW.String()
	result.Outputs["duration"] = elapsed.String()

	if err != nil {
		result.Status = model.StepFailed
		result.ErrorMessage = err.Error()
		return result
	}

	// JSON auto-parse: if stdout is a JSON object or array, set outputs.result
	if parsed, ok := tryParseJSONOutput(stdoutW.String()); ok {
		result.Outputs["result"] = parsed
	}

	result.Status = model.StepSucceeded
	return result
}

// startAndWait starts the command and waits for completion or context cancellation.
// On cancel/timeout, it kills the process group with a 2-second grace period.
func startAndWait(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	select {
	case err := <-waitDone:
		return err
	case <-ctx.Done():
		killProcessGroup(cmd, 2*time.Second)
		return <-waitDone
	}
}
