package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/shellutil"
	"github.com/sjzar/reed/pkg/truncate"
)

// ---------------------------------------------------------------------------
// bashTool — runs a command via bash -c
// ---------------------------------------------------------------------------

type bashArgs struct {
	Command    string `json:"command"`
	WorkDir    string `json:"workdir"`
	Timeout    int    `json:"timeout"`
	Background bool   `json:"background"`
}

type bashTool struct {
	shellPath string
	rtkPath   string // empty if rtk is not installed
	runCmd    func(ctx context.Context, name string, args []string, workDir string, env []string) (string, string, int, error)
}

// bashDefaultTimeout is the default timeout for bash commands (120s).
// Longer than other tools because build/test commands are common.
const bashDefaultTimeout = 120 * time.Second

// resolveShell finds a bash binary. Returns error if bash is not found.
// On Windows, only known bash distributions are checked (Git Bash, MSYS2, Cygwin).
// No fallback to sh/pwsh — the tool is bash, commands are bash syntax.
func resolveShell() (string, error) {
	return shellutil.ResolveBash()
}

func NewBashTool() Tool {
	shellPath, _ := resolveShell()
	rtkPath, _ := exec.LookPath("rtk")
	return &bashTool{shellPath: shellPath, rtkPath: rtkPath, runCmd: execRun}
}

func (t *bashTool) Group() ToolGroup { return GroupCore }

func (t *bashTool) Def() model.ToolDef {
	return model.ToolDef{
		Name: "bash",
		Description: "Execute a shell command in the working directory.\n" +
			"Use when: running build/test commands, git operations, installing packages, or system tasks that no dedicated tool covers.\n" +
			"Don't use when: reading files (use read), searching code (use search), writing/editing files (use write/edit). Dedicated tools are faster, safer, and can run in parallel.\n" +
			"Set background=true for long-running commands (builds, tests) to continue working while they execute.\n" +
			`Example: {"command": "go test -v ./..."} or {"command": "npm run build", "background": true}`,
		Summary: "Execute a shell command. Use for build/test/git/system tasks only.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":    map[string]any{"type": "string", "description": "Shell command to execute"},
				"workdir":    map[string]any{"type": "string", "description": "Working directory (optional)"},
				"timeout":    map[string]any{"type": "integer", "description": "Timeout in seconds (optional, default 120)"},
				"background": map[string]any{"type": "boolean", "description": "Run in background, return immediately (default: false)"},
			},
			"required": []string{"command"},
		},
	}
}

func (t *bashTool) Prepare(ctx context.Context, req CallRequest) (*PreparedCall, error) {
	if t.shellPath == "" {
		return nil, fmt.Errorf("bash not found on this system")
	}
	var p bashArgs
	if err := json.Unmarshal(req.RawArgs, &p); err != nil {
		return nil, fmt.Errorf("parse bash args: %w", err)
	}
	if p.Command == "" {
		return nil, fmt.Errorf("command is required")
	}

	// Resolve workdir via path policy
	workDir := p.WorkDir
	if workDir == "" {
		workDir = req.Context.Cwd
	}
	if workDir != "" {
		resolved, err := ResolvePath(req.Context.Cwd, workDir)
		if err != nil {
			return nil, fmt.Errorf("resolve workdir: %w", err)
		}
		if err := checkWriteAccess(ctx, resolved); err != nil {
			return nil, err
		}
		workDir = resolved
	} else {
		// No workdir available — fail-closed regardless of security context.
		return nil, fmt.Errorf("bash: no working directory available")
	}
	p.WorkDir = workDir

	timeout := bashDefaultTimeout
	if p.Timeout > 0 {
		timeout = time.Duration(p.Timeout) * time.Second
	}

	mode := ExecModeSync
	if p.Background {
		mode = ExecModeAsync
	}

	return &PreparedCall{
		ToolCallID: req.ToolCallID,
		Name:       req.Name,
		RawArgs:    req.RawArgs,
		Parsed:     p,
		Plan: ExecutionPlan{
			Mode:    mode,
			Timeout: timeout,
			Policy:  ParallelSafe,
		},
	}, nil
}

func (t *bashTool) Execute(ctx context.Context, call *PreparedCall) (*Result, error) {
	p, ok := call.Parsed.(bashArgs)
	if !ok {
		return nil, fmt.Errorf("internal: unexpected parsed type %T", call.Parsed)
	}
	command := p.Command
	if t.rtkPath != "" {
		command = t.rtkPath + " " + command
	}

	env := mergeEnv(call.Env)
	stdout, stderr, exitCode, err := t.runCmd(ctx, t.shellPath, []string{"-c", command}, p.WorkDir, env)
	if err != nil {
		return nil, fmt.Errorf("bash: %w", err)
	}

	output := formatCommandOutput(stdout, stderr, exitCode)
	truncated, info := truncate.Tail(output, DefaultMaxLines, DefaultMaxBytes)
	if info.Truncated {
		if info.ByteLimitHit && info.ShownLines == 0 {
			truncated = "[output exceeds size limit]\n" + truncated
		} else {
			truncated = fmt.Sprintf("[output truncated: showing last %d of %d lines]\n%s", info.ShownLines, info.TotalLines, truncated)
		}
	}

	if exitCode != 0 {
		return &Result{Content: model.TextContent(truncated), IsError: true}, nil
	}
	return TextResult(truncated), nil
}

// ---------------------------------------------------------------------------
// exec implementation + helpers
// ---------------------------------------------------------------------------

// execRun is the default command runner using os/exec.
func execRun(ctx context.Context, name string, args []string, workDir string, env []string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	if len(env) > 0 {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			err = nil // non-zero exit is not a Go error for us
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// mergeEnv builds a deduplicated env slice from OS env + workflow env.
func mergeEnv(workflowEnv map[string]string) []string {
	m := make(map[string]string, len(os.Environ())+len(workflowEnv))
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	for k, v := range workflowEnv {
		m[k] = v
	}
	result := make([]string, 0, len(m))
	for k, v := range m {
		result = append(result, k+"="+v)
	}
	return result
}

func formatCommandOutput(stdout, stderr string, exitCode int) string {
	var b strings.Builder
	if stdout != "" {
		b.WriteString(stdout)
	}
	if stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[stderr]\n")
		b.WriteString(stderr)
	}
	if exitCode != 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[exit_code: %d]", exitCode)
	}
	if b.Len() == 0 {
		return "(no output)"
	}
	return b.String()
}
