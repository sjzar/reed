//go:build !windows

package worker

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/sjzar/reed/internal/shellutil"
)

// resolveShellPlatform resolves the shell invocation for Unix/macOS.
// For "uses: bash": strict bash via shellutil.ResolveBash(), no fallback.
// For "uses: shell"/"run": bash if found, else sh.
// Named shells: LookPath for sh, zsh, bash.
func resolveShellPlatform(uses, requested string) (shellInvocation, error) {
	if requested != "" {
		return resolveNamedShellUnix(requested)
	}

	// Default resolution based on uses
	switch uses {
	case "bash":
		p, err := shellutil.ResolveBash()
		if err != nil {
			return shellInvocation{}, fmt.Errorf("uses: bash requires bash: %w", err)
		}
		return shellInvocation{path: p, args: []string{"-c"}}, nil
	default:
		// uses: shell / run — bash if found, else sh
		if p, err := shellutil.ResolveBash(); err == nil {
			return shellInvocation{path: p, args: []string{"-c"}}, nil
		}
		p, err := exec.LookPath("sh")
		if err != nil {
			return shellInvocation{}, fmt.Errorf("no shell found: neither bash nor sh on PATH")
		}
		return shellInvocation{path: p, args: []string{"-c"}}, nil
	}
}

func resolveNamedShellUnix(id string) (shellInvocation, error) {
	switch id {
	case "bash":
		p, err := shellutil.ResolveBash()
		if err != nil {
			return shellInvocation{}, err
		}
		return shellInvocation{path: p, args: []string{"-c"}}, nil
	case "sh":
		p, err := exec.LookPath("sh")
		if err != nil {
			return shellInvocation{}, fmt.Errorf("sh not found: %w", err)
		}
		return shellInvocation{path: p, args: []string{"-c"}}, nil
	case "zsh":
		p, err := exec.LookPath("zsh")
		if err != nil {
			return shellInvocation{}, fmt.Errorf("zsh not found: %w", err)
		}
		return shellInvocation{path: p, args: []string{"-c"}}, nil
	default:
		return shellInvocation{}, fmt.Errorf("unknown shell %q (supported: bash, sh, zsh)", id)
	}
}

// setSysProcAttr configures the command to run in its own process group,
// so we can kill the entire tree on timeout/cancel.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGTERM to the process group, waits for grace,
// then sends SIGKILL if the process is still alive.
func killProcessGroup(cmd *exec.Cmd, grace time.Duration) {
	if cmd.Process == nil {
		return
	}
	pgid := -cmd.Process.Pid
	// SIGTERM the whole group; ignore ESRCH (already dead).
	_ = syscall.Kill(pgid, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		// cmd.Wait() will be called by the caller; we just watch the process.
		cmd.Process.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(grace):
		_ = syscall.Kill(pgid, syscall.SIGKILL)
	}
}
