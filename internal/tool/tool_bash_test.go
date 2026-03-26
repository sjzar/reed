package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/security"
)

// bashCtx returns a context with a full-access guard for bash tests that don't test security.
func bashCtx() context.Context {
	return security.WithChecker(context.Background(), security.New(security.ProfileFull, ""))
}

func bashCallRequest(args []byte) CallRequest {
	return CallRequest{
		RawArgs: args,
		Context: RuntimeContext{Set: true, Cwd: "/tmp"},
	}
}

func TestBashTool(t *testing.T) {
	mock := &mockRunCmd{stdout: "bash output"}
	bt := &bashTool{shellPath: "/bin/bash", runCmd: mock.run}

	if bt.Def().Name != "bash" {
		t.Fatalf("expected name bash, got %s", bt.Def().Name)
	}

	args, _ := json.Marshal(map[string]any{"command": "echo hello"})
	pc, err := bt.Prepare(bashCtx(), bashCallRequest(args))
	if err != nil {
		t.Fatalf("unexpected prepare error: %v", err)
	}
	result, err := bt.Execute(context.Background(), pc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) == 0 || result.Content[0].Text != "bash output" {
		t.Fatalf("expected 'bash output', got %v", result.Content)
	}
}

func TestBashTool_EnvPassthrough(t *testing.T) {
	mock := &mockRunCmd{stdout: "ok"}
	bt := &bashTool{shellPath: "/bin/bash", runCmd: mock.run}
	args, _ := json.Marshal(map[string]any{"command": "echo $REED_RUN_SKILL_DIR"})
	pc, err := bt.Prepare(bashCtx(), bashCallRequest(args))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	// Simulate workflow env on PreparedCall
	pc.Env = map[string]string{"REED_RUN_SKILL_DIR": "/tmp/skills"}
	_, err = bt.Execute(context.Background(), pc)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Verify env was passed to runner
	found := false
	for _, e := range mock.lastEnv {
		if e == "REED_RUN_SKILL_DIR=/tmp/skills" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected REED_RUN_SKILL_DIR in env passed to runner")
	}
}

func TestBashToolMissingCommand(t *testing.T) {
	bt := &bashTool{shellPath: "/bin/bash", runCmd: (&mockRunCmd{}).run}
	args, _ := json.Marshal(map[string]any{})
	_, err := bt.Prepare(context.Background(), CallRequest{RawArgs: args})
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestBashTool_NonZeroExit(t *testing.T) {
	mock := &mockRunCmd{stdout: "out", stderr: "err", exitCode: 1}
	bt := &bashTool{shellPath: "/bin/bash", runCmd: mock.run}
	args, _ := json.Marshal(map[string]any{"command": "false"})
	pc, err := bt.Prepare(bashCtx(), bashCallRequest(args))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	result, err := bt.Execute(context.Background(), pc)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for non-zero exit code")
	}
}

func TestBashTool_RunnerError(t *testing.T) {
	mock := &mockRunCmd{err: errors.New("exec failed")}
	bt := &bashTool{shellPath: "/bin/bash", runCmd: mock.run}
	args, _ := json.Marshal(map[string]any{"command": "bad"})
	pc, err := bt.Prepare(bashCtx(), bashCallRequest(args))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	_, err = bt.Execute(context.Background(), pc)
	if err == nil {
		t.Fatal("expected error from runner")
	}
}

func TestBashTool_WorkdirOutsideRoots(t *testing.T) {
	bt := &bashTool{shellPath: "/bin/bash", runCmd: (&mockRunCmd{stdout: "ok"}).run}
	args, _ := json.Marshal(map[string]any{"command": "ls", "workdir": "/etc"})
	// Use a restrictive guard that only allows /home/user/project
	guard := security.New(security.ProfileWorkdir, "/home/user/project")
	ctx := security.WithChecker(context.Background(), guard)
	_, err := bt.Prepare(ctx, CallRequest{
		RawArgs: args,
		Context: RuntimeContext{
			Set: true,
			Cwd: "/home/user/project",
		},
	})
	if err == nil {
		t.Fatal("expected error for workdir outside allowed roots")
	}
}

func TestBashTool_Timeout(t *testing.T) {
	bt := &bashTool{shellPath: "/bin/bash", runCmd: (&mockRunCmd{stdout: "ok"}).run}
	args, _ := json.Marshal(map[string]any{"command": "sleep 1", "timeout": 5})
	pc, err := bt.Prepare(bashCtx(), bashCallRequest(args))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if pc.Plan.Timeout != 5*1e9 { // 5 seconds in nanoseconds
		t.Errorf("expected timeout 5s, got %v", pc.Plan.Timeout)
	}
}

func TestFormatCommandOutput(t *testing.T) {
	tests := []struct {
		name     string
		stdout   string
		stderr   string
		exitCode int
		want     string
	}{
		{"empty", "", "", 0, "(no output)"},
		{"stdout only", "hello", "", 0, "hello"},
		{"stderr only", "", "err", 0, "[stderr]\nerr"},
		{"both", "out", "err", 0, "out\n[stderr]\nerr"},
		{"exit code", "out", "", 1, "out\n[exit_code: 1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCommandOutput(tt.stdout, tt.stderr, tt.exitCode)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveShell_Unix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	p, err := resolveShell()
	if err != nil {
		t.Fatalf("resolveShell failed: %v", err)
	}
	if !strings.HasSuffix(p, "bash") && !strings.HasSuffix(p, "bash.exe") {
		t.Errorf("expected path ending in bash, got %q", p)
	}
}

func TestBashTool_NoBash(t *testing.T) {
	// Construct a bashTool with empty shellPath to simulate bash not found
	bt := &bashTool{runCmd: (&mockRunCmd{}).run, shellPath: ""}
	args, _ := json.Marshal(map[string]any{"command": "echo hello"})
	_, err := bt.Prepare(context.Background(), CallRequest{RawArgs: args})
	if err == nil {
		t.Fatal("expected error for missing bash")
	}
	if !strings.Contains(err.Error(), "bash not found") {
		t.Errorf("expected 'bash not found' error, got: %v", err)
	}
}

func TestBashTool_NoWorkDir(t *testing.T) {
	bt := &bashTool{shellPath: "/bin/bash", runCmd: (&mockRunCmd{stdout: "ok"}).run}
	args, _ := json.Marshal(map[string]any{"command": "echo hello"})
	// No workdir in args, no cwd in context — should fail
	_, err := bt.Prepare(bashCtx(), CallRequest{RawArgs: args})
	if err == nil {
		t.Fatal("expected error for missing working directory")
	}
	if !strings.Contains(err.Error(), "no working directory") {
		t.Errorf("expected 'no working directory' error, got: %v", err)
	}
}

type mockRunCmd struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
	lastCmd  string
	lastArgs []string
	lastEnv  []string
}

func (m *mockRunCmd) run(_ context.Context, name string, args []string, _ string, env []string) (string, string, int, error) {
	m.lastCmd = name
	m.lastArgs = args
	m.lastEnv = env
	return m.stdout, m.stderr, m.exitCode, m.err
}
