package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

func TestShellWorker_BusEvents(t *testing.T) {
	b := bus.New()
	defer b.Close()
	topic := bus.StepOutputTopic("sr_bus")
	sub := b.Subscribe(topic, 100)
	defer sub.Unsubscribe()

	w := &ShellWorker{}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_bus",
		JobID:     "j",
		StepID:    "s",
		With:      map[string]any{"run": "echo hello"},
		Bus:       b,
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}

	// Collect messages
	var statusMsgs []string
	var textMsgs []string
	for {
		select {
		case msg := <-sub.Ch():
			switch msg.Type {
			case "status":
				if sp, ok := msg.Payload.(bus.StatusPayload); ok {
					statusMsgs = append(statusMsgs, sp.Message)
				}
			case "text":
				if tp, ok := msg.Payload.(bus.TextPayload); ok {
					textMsgs = append(textMsgs, tp.Delta)
				}
			}
		default:
			goto done
		}
	}
done:

	// Should have shell_start and shell_end status messages
	if len(statusMsgs) < 2 {
		t.Errorf("expected at least 2 status messages, got %d: %v", len(statusMsgs), statusMsgs)
	}
	foundStart := false
	foundEnd := false
	for _, msg := range statusMsgs {
		if strings.HasPrefix(msg, "shell_start") {
			foundStart = true
		}
		if strings.HasPrefix(msg, "shell_end") {
			foundEnd = true
		}
	}
	if !foundStart {
		t.Error("missing shell_start status")
	}
	if !foundEnd {
		t.Error("missing shell_end status")
	}

	// Should have text output
	combined := strings.Join(textMsgs, "")
	if !strings.Contains(combined, "hello") {
		t.Errorf("text output = %q, want to contain hello", combined)
	}
}

func TestHTTPWorker_BusEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	b := bus.New()
	defer b.Close()
	topic := bus.StepOutputTopic("sr_bus")
	sub := b.Subscribe(topic, 100)
	defer sub.Unsubscribe()

	w := newHTTPWorker()
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_bus",
		JobID:     "j",
		StepID:    "s",
		Uses:      "http",
		With:      map[string]any{"url": srv.URL},
		Bus:       b,
	})
	if result.Status != model.StepSucceeded {
		t.Fatalf("status = %s, error: %s", result.Status, result.ErrorMessage)
	}

	var statusMsgs []string
	for {
		select {
		case msg := <-sub.Ch():
			if msg.Type == "status" {
				if sp, ok := msg.Payload.(bus.StatusPayload); ok {
					statusMsgs = append(statusMsgs, sp.Message)
				}
			}
		default:
			goto done
		}
	}
done:

	if len(statusMsgs) < 2 {
		t.Errorf("expected at least 2 status messages, got %d: %v", len(statusMsgs), statusMsgs)
	}
	foundStart := false
	foundEnd := false
	for _, msg := range statusMsgs {
		if strings.HasPrefix(msg, "http_start") {
			foundStart = true
		}
		if strings.HasPrefix(msg, "http_end") {
			foundEnd = true
		}
	}
	if !foundStart {
		t.Error("missing http_start status")
	}
	if !foundEnd {
		t.Error("missing http_end status")
	}
}

func TestAgentWorker_SessionKeyAndIDMutualExclusion(t *testing.T) {
	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model"},
			},
		},
	}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{
			"agent":       "coder",
			"prompt":      "test",
			"session_key": "key1",
			"session_id":  "id1",
		},
	})
	if result.Status != model.StepFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
	if !strings.Contains(result.ErrorMessage, "both session_key and session_id") {
		t.Errorf("error = %q", result.ErrorMessage)
	}
}
