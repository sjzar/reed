package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/pkg/logutil"
)

// Event is a single JSONL log entry.
type Event struct {
	Timestamp string `json:"ts"`
	ProcessID string `json:"processID"`
	RunID     string `json:"runID,omitempty"`
	StepRunID string `json:"stepRunID,omitempty"`
	JobID     string `json:"jobID,omitempty"`
	StepID    string `json:"stepID,omitempty"`
	Type      string `json:"type"`
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
}

// EventSink is the minimal interface for appending events.
// Close() is NOT part of this interface; the owner (reed.Manager) calls it
// on the concrete type.
type EventSink interface {
	Append(ctx context.Context, event Event) error
}

// JSONLWriter writes events as JSONL to a Process-level event log file.
// Path: <logDir>/<processID>.events.jsonl
type JSONLWriter struct {
	mu     sync.Mutex
	writer io.WriteCloser
}

// NewJSONLWriter creates a new JSONL writer for the given process.
func NewJSONLWriter(logDir, processID string) (*JSONLWriter, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	path := filepath.Join(logDir, fmt.Sprintf("%s.events.jsonl", processID))
	w := logutil.NewRotatingWriter(path)
	return &JSONLWriter{writer: w}, nil
}

// Append writes a single event as a JSON line.
func (w *JSONLWriter) Append(_ context.Context, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.writer.Write(data)
	return err
}

// Close flushes and closes the underlying writer.
func (w *JSONLWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Close()
}

// Event type constants.
const (
	EventStepStarted   = "step_started"
	EventStepFinished  = "step_finished"
	EventStepFailed    = "step_failed"
	EventStopRequested = "stop_requested"
	EventRunFinalized  = "run_finalized"
)

// NewStepEvent creates an event for step lifecycle changes.
func NewStepEvent(ts time.Time, processID, runID, stepRunID, jobID, stepID, eventType, status string) Event {
	return Event{
		Timestamp: ts.Format(time.RFC3339Nano),
		ProcessID: processID,
		RunID:     runID,
		StepRunID: stepRunID,
		JobID:     jobID,
		StepID:    stepID,
		Type:      eventType,
		Status:    status,
	}
}

// NewRunEvent creates an event for run lifecycle changes.
func NewRunEvent(ts time.Time, processID, runID, eventType, status, message string) Event {
	return Event{
		Timestamp: ts.Format(time.RFC3339Nano),
		ProcessID: processID,
		RunID:     runID,
		Type:      eventType,
		Status:    status,
		Message:   message,
	}
}

// lifecycleToEvent converts a bus.LifecyclePayload to an Event.
func lifecycleToEvent(p bus.LifecyclePayload) Event {
	return Event{
		Timestamp: p.Timestamp,
		ProcessID: p.ProcessID,
		RunID:     p.RunID,
		StepRunID: p.StepRunID,
		JobID:     p.JobID,
		StepID:    p.StepID,
		Type:      p.Type,
		Status:    p.Status,
		Message:   p.Message,
		Error:     p.Error,
	}
}
