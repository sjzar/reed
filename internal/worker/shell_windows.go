//go:build windows

package worker

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/sjzar/reed/internal/shellutil"
)

// resolveShellPlatform resolves the shell invocation for Windows.
// For "uses: bash": strict bash via shellutil.ResolveBash(), no fallback.
// For "uses: shell"/"run": powershell.
// Named shells: powershell, cmd, bash.
func resolveShellPlatform(uses, requested string) (shellInvocation, error) {
	if requested != "" {
		return resolveNamedShellWindows(requested)
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
		// uses: shell / run — powershell
		return shellInvocation{
			path: "powershell",
			args: []string{"-NoProfile", "-NonInteractive", "-Command"},
		}, nil
	}
}

func resolveNamedShellWindows(id string) (shellInvocation, error) {
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
	case "powershell":
		return shellInvocation{
			path: "powershell",
			args: []string{"-NoProfile", "-NonInteractive", "-Command"},
		}, nil
	case "cmd":
		return shellInvocation{path: "cmd", args: []string{"/C"}}, nil
	default:
		return shellInvocation{}, fmt.Errorf("unknown shell %q (supported: bash, sh, zsh, powershell, cmd)", id)
	}
}

// setSysProcAttr is a no-op on Windows (no process group support via Setpgid).
func setSysProcAttr(cmd *exec.Cmd) {}

// killProcessGroup forcefully kills the process on Windows.
// There is no process group signal equivalent; we just kill the main process.
func killProcessGroup(cmd *exec.Cmd, grace time.Duration) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
