package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJSONLWriter_AppendAndRead(t *testing.T) {
	dir := t.TempDir()
	w, err := NewJSONLWriter(dir, "test_001")
	if err != nil {
		t.Fatalf("NewJSONLWriter: %v", err)
	}
	defer w.Close()

	ts := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	events := []Event{
		NewStepEvent(ts, "proc_1", "run_1", "sr_1", "build", "s1", EventStepStarted, "RUNNING"),
		NewStepEvent(ts, "proc_1", "run_1", "sr_1", "build", "s1", EventStepFinished, "SUCCEEDED"),
		NewRunEvent(ts, "proc_1", "run_1", EventRunFinalized, "SUCCEEDED", "all steps done"),
	}

	for _, ev := range events {
		if err := w.Append(context.Background(), ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// Read back and verify
	path := filepath.Join(dir, "test_001.events.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var read []Event
	for scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		read = append(read, ev)
	}

	if len(read) != 3 {
		t.Fatalf("read %d events, want 3", len(read))
	}
	if read[0].Type != EventStepStarted {
		t.Errorf("event[0].Type = %q, want %q", read[0].Type, EventStepStarted)
	}
	if read[1].Status != "SUCCEEDED" {
		t.Errorf("event[1].Status = %q, want SUCCEEDED", read[1].Status)
	}
	if read[2].Message != "all steps done" {
		t.Errorf("event[2].Message = %q", read[2].Message)
	}
}

func TestJSONLWriter_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	w, err := NewJSONLWriter(dir, "x")
	if err != nil {
		t.Fatalf("NewJSONLWriter: %v", err)
	}

	// Write an event to trigger file creation (lumberjack creates lazily)
	ts := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	if err := w.Append(context.Background(), NewRunEvent(ts, "proc_x", "run_1", EventRunFinalized, "SUCCEEDED", "done")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	w.Close()

	if _, err := os.Stat(filepath.Join(dir, "x.events.jsonl")); err != nil {
		t.Errorf("expected log file to exist: %v", err)
	}
}
