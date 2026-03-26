package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

func TestRouter_ShellRoute(t *testing.T) {
	r := NewRouter()
	for _, uses := range []string{"shell", "bash", "run"} {
		t.Run(uses, func(t *testing.T) {
			result := r.Execute(context.Background(), engine.StepPayload{
				StepRunID: "sr_1",
				JobID:     "j",
				StepID:    "s",
				Uses:      uses,
				With:      map[string]any{"run": "echo hello"},
			})
			if result.Status != model.StepSucceeded {
				t.Errorf("uses=%q: status = %s, want SUCCEEDED; error: %s", uses, result.Status, result.ErrorMessage)
			}
			stdout := result.Outputs["stdout"].(string)
			if strings.TrimSpace(stdout) != "hello" {
				t.Errorf("uses=%q: stdout = %q, want hello", uses, stdout)
			}
		})
	}
}

func TestRouter_UnknownUses(t *testing.T) {
	r := NewRouter()
	result := r.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1",
		JobID:     "j",
		StepID:    "s",
		Uses:      "agent",
		With:      map[string]any{},
	})
	if result.Status != model.StepFailed {
		t.Errorf("status = %s, want FAILED", result.Status)
	}
	if !strings.Contains(result.ErrorMessage, "unknown uses") {
		t.Errorf("error = %q, want to contain 'unknown uses'", result.ErrorMessage)
	}
}

func TestRouter_PreservesIDs(t *testing.T) {
	r := NewRouter()
	result := r.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_test",
		JobID:     "job_test",
		StepID:    "step_test",
		Uses:      "shell",
		With:      map[string]any{"run": "echo ok"},
	})
	if result.StepRunID != "sr_test" {
		t.Errorf("StepRunID = %q, want sr_test", result.StepRunID)
	}
	if result.JobID != "job_test" {
		t.Errorf("JobID = %q, want job_test", result.JobID)
	}
	if result.StepID != "step_test" {
		t.Errorf("StepID = %q, want step_test", result.StepID)
	}
}

func TestShellWorker_EnvPassthrough(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1",
		JobID:     "j",
		StepID:    "s",
		Uses:      "shell",
		With:      map[string]any{"run": "echo $MY_VAR"},
		Env:       map[string]string{"MY_VAR": "test_value"},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	stdout := result.Outputs["stdout"].(string)
	if strings.TrimSpace(stdout) != "test_value" {
		t.Errorf("stdout = %q, want test_value", stdout)
	}
}

func TestShellWorker_WorkDir(t *testing.T) {
	dir := t.TempDir()
	// Resolve symlinks so the comparison matches (macOS /var → /private/var)
	dir, _ = filepath.EvalSymlinks(dir)
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1",
		JobID:     "j",
		StepID:    "s",
		Uses:      "shell",
		With:      map[string]any{"run": "pwd"},
		WorkDir:   dir,
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	stdout := strings.TrimSpace(result.Outputs["stdout"].(string))
	if stdout != dir {
		t.Errorf("stdout = %q, want %q", stdout, dir)
	}
}

func TestShellWorker_StderrCapture(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1",
		JobID:     "j",
		StepID:    "s",
		With:      map[string]any{"run": "echo error_msg >&2"},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s", result.Status)
	}
	stderr := result.Outputs["stderr"].(string)
	if !strings.Contains(stderr, "error_msg") {
		t.Errorf("stderr = %q, want to contain error_msg", stderr)
	}
}

func TestShellWorker_DurationOutput(t *testing.T) {
	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1",
		JobID:     "j",
		StepID:    "s",
		With:      map[string]any{"run": "echo ok"},
	})
	if result.Status != model.StepSucceeded {
		t.Fatal("expected SUCCEEDED")
	}
	dur, ok := result.Outputs["duration"].(string)
	if !ok || dur == "" {
		t.Error("expected non-empty duration output")
	}
}

func TestRouter_HTTPRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"routed":true}`)
	}))
	defer srv.Close()

	r := NewRouter()
	result := r.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1",
		JobID:     "j",
		StepID:    "s",
		Uses:      "http",
		With:      map[string]any{"url": srv.URL},
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}
	m, ok := result.Outputs["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not parsed, got %T", result.Outputs["result"])
	}
	if m["routed"] != true {
		t.Errorf("result.routed = %v, want true", m["routed"])
	}
}
