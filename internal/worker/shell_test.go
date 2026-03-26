package worker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

func TestShellWorker_EchoCommand(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1",
		JobID:     "j",
		StepID:    "s",
		With:      map[string]any{"run": "echo hello"},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, want SUCCEEDED", result.Status)
	}
	stdout := result.Outputs["stdout"].(string)
	if strings.TrimSpace(stdout) != "hello" {
		t.Errorf("stdout = %q, want hello", stdout)
	}
}

func TestShellWorker_FailingCommand(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_2",
		JobID:     "j",
		StepID:    "s",
		With:      map[string]any{"run": "exit 1"},
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
	if result.ErrorMessage == "" {
		t.Error("expected error message")
	}
}

func TestShellWorker_NoCommand(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_3",
		JobID:     "j",
		StepID:    "s",
		With:      map[string]any{},
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
	if !strings.Contains(result.ErrorMessage, "no 'run' or 'cmd'") {
		t.Errorf("error = %q", result.ErrorMessage)
	}
}

func TestShellWorker_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	w := &ShellWorker{}
	result := w.Execute(ctx, engine.StepPayload{
		StepRunID: "sr_4",
		JobID:     "j",
		StepID:    "s",
		With:      map[string]any{"run": "sleep 10"},
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
}

func TestShellWorker_Timeout(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_5",
		JobID:     "j",
		StepID:    "s",
		With:      map[string]any{"run": "sleep 10"},
		Timeout:   1, // 1 second; sleep 10 will be killed
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
}

func TestShellWorker_CmdAlias(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_6",
		JobID:     "j",
		StepID:    "s",
		With:      map[string]any{"cmd": "echo world"},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, want SUCCEEDED", result.Status)
	}
	stdout := result.Outputs["stdout"].(string)
	if strings.TrimSpace(stdout) != "world" {
		t.Errorf("stdout = %q, want world", stdout)
	}
}

func TestShellWorker_JSONAutoParseObject(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		With: map[string]any{"run": `echo '{"name":"reed","version":1}'`},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	m, ok := result.Outputs["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not parsed as map, got %T", result.Outputs["result"])
	}
	if m["name"] != "reed" {
		t.Errorf("result.name = %v, want reed", m["name"])
	}
}

func TestShellWorker_JSONAutoParseArray(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		With: map[string]any{"run": `echo '[1,2,3]'`},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	arr, ok := result.Outputs["result"].([]any)
	if !ok {
		t.Fatalf("result not parsed as array, got %T", result.Outputs["result"])
	}
	if len(arr) != 3 {
		t.Errorf("len(result) = %d, want 3", len(arr))
	}
}

func TestShellWorker_JSONAutoParseScalarNotParsed(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		With: map[string]any{"run": `echo '42'`},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	if _, ok := result.Outputs["result"]; ok {
		t.Error("scalar JSON should not be parsed into result")
	}
}

func TestShellWorker_JSONAutoParseNonJSON(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		With: map[string]any{"run": `echo 'not json at all'`},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	if _, ok := result.Outputs["result"]; ok {
		t.Error("non-JSON should not produce result")
	}
}

func TestShellWorker_EnvInheritance(t *testing.T) {
	// PATH must be inherited from the OS environment
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		With: map[string]any{"run": "echo $PATH"},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	stdout := strings.TrimSpace(result.Outputs["stdout"].(string))
	if stdout == "" {
		t.Error("PATH should be inherited from OS env, got empty")
	}
}

func TestShellWorker_WorkDirRelativePath(t *testing.T) {
	// Create a temp dir and use a relative path to it
	dir := t.TempDir()
	// Create a subdir
	sub := filepath.Join(dir, "child")
	os.Mkdir(sub, 0o755)

	// Change to parent so "child" is a valid relative path
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		With:    map[string]any{"run": "pwd"},
		WorkDir: "child",
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	stdout := strings.TrimSpace(result.Outputs["stdout"].(string))
	if !strings.HasSuffix(stdout, "child") {
		t.Errorf("stdout = %q, want to end with child", stdout)
	}
}

func TestShellWorker_WorkDirSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unreliable on Windows")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	os.Mkdir(real, 0o755)
	link := filepath.Join(dir, "link")
	os.Symlink(real, link)

	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		With:    map[string]any{"run": "pwd -P"},
		WorkDir: link,
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	stdout := strings.TrimSpace(result.Outputs["stdout"].(string))
	// The resolved real path should be used
	realResolved, _ := filepath.EvalSymlinks(real)
	if stdout != realResolved {
		t.Errorf("stdout = %q, want %q (resolved symlink)", stdout, realResolved)
	}
}

func TestShellWorker_ProcessGroupKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group kill not supported on Windows")
	}
	w := &ShellWorker{}
	// Spawn a shell that starts a background child; timeout should kill the whole group
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		With:    map[string]any{"run": "sleep 60 & wait"},
		Timeout: 1,
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED (timeout)", result.Status)
	}
}

// ---------------------------------------------------------------------------
// resolveShellInvocation unit tests (pure function, no exec)
// ---------------------------------------------------------------------------

func TestResolveShellInvocation_BashDefault(t *testing.T) {
	inv, err := resolveShellInvocation("bash", "")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}
	if !strings.HasSuffix(inv.path, "bash") {
		t.Errorf("path = %q, want to end with bash", inv.path)
	}
	if len(inv.args) != 1 || inv.args[0] != "-c" {
		t.Errorf("args = %v, want [-c]", inv.args)
	}
}

func TestResolveShellInvocation_BashRejectsOverride(t *testing.T) {
	_, err := resolveShellInvocation("bash", "sh")
	if err == nil {
		t.Fatal("expected error for uses:bash + shell:sh")
	}
	if !strings.Contains(err.Error(), "does not allow shell override") {
		t.Errorf("error = %q, want 'does not allow shell override'", err)
	}
}

func TestResolveShellInvocation_BashAllowsBashOverride(t *testing.T) {
	inv, err := resolveShellInvocation("bash", "bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}
	if !strings.HasSuffix(inv.path, "bash") {
		t.Errorf("path = %q, want to end with bash", inv.path)
	}
}

func TestResolveShellInvocation_ShellDefault(t *testing.T) {
	inv, err := resolveShellInvocation("shell", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// On unix: bash or sh; on windows: powershell
	if inv.path == "" {
		t.Fatal("path should not be empty")
	}
}

func TestResolveShellInvocation_ShellWithZsh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("zsh not typically available on Windows")
	}
	inv, err := resolveShellInvocation("shell", "zsh")
	if err != nil {
		t.Skipf("zsh not available: %v", err)
	}
	if !strings.HasSuffix(inv.path, "zsh") {
		t.Errorf("path = %q, want to end with zsh", inv.path)
	}
	if len(inv.args) != 1 || inv.args[0] != "-c" {
		t.Errorf("args = %v, want [-c]", inv.args)
	}
}

func TestResolveShellInvocation_UnknownShell(t *testing.T) {
	_, err := resolveShellInvocation("shell", "fish")
	if err == nil {
		t.Fatal("expected error for unknown shell")
	}
	if !strings.Contains(err.Error(), "unknown shell") {
		t.Errorf("error = %q, want 'unknown shell'", err)
	}
}

func TestResolveShellInvocation_RunDefault(t *testing.T) {
	inv, err := resolveShellInvocation("run", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.path == "" {
		t.Fatal("path should not be empty")
	}
}

// ---------------------------------------------------------------------------
// Integration tests: shell override via StepPayload.Shell
// ---------------------------------------------------------------------------

func TestShellWorker_ShellOverrideSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh not typically available on Windows")
	}
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses:  "shell",
		Shell: "sh",
		With:  map[string]any{"run": "echo from_sh"},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	stdout := strings.TrimSpace(result.Outputs["stdout"].(string))
	if stdout != "from_sh" {
		t.Errorf("stdout = %q, want from_sh", stdout)
	}
}

func TestShellWorker_UsesBashStrict(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses: "bash",
		With: map[string]any{"run": "echo from_bash"},
	})
	if result.Status != model.StepSucceeded {
		t.Skipf("bash not available: %s", result.ErrorMessage)
	}
	stdout := strings.TrimSpace(result.Outputs["stdout"].(string))
	if stdout != "from_bash" {
		t.Errorf("stdout = %q, want from_bash", stdout)
	}
}

func TestShellWorker_UsesBashRejectsShellOverride(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j", StepID: "s",
		Uses:  "bash",
		Shell: "sh",
		With:  map[string]any{"run": "echo nope"},
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
	if !strings.Contains(result.ErrorMessage, "does not allow shell override") {
		t.Errorf("error = %q", result.ErrorMessage)
	}
}
