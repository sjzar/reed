package logutil

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFollower_ReadsNewLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	// Write initial content
	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := NewFollower(path, false)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var mu sync.Mutex
	var lines []string

	go func() {
		_ = f.Follow(ctx, func(line string) {
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
		})
	}()

	// Give follower time to read initial content
	time.Sleep(200 * time.Millisecond)

	// Append new line
	af, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	af.WriteString("line3\n")
	af.Close()

	// Wait for poll/fsnotify to pick it up
	time.Sleep(700 * time.Millisecond)
	cancel()

	mu.Lock()
	defer mu.Unlock()

	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %v", len(lines), lines)
	}
	if lines[0] != "line1" {
		t.Errorf("lines[0] = %q, want line1", lines[0])
	}
	if lines[2] != "line3" {
		t.Errorf("lines[2] = %q, want line3", lines[2])
	}
}

func TestFollower_StartFromEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	// Write initial content
	if err := os.WriteFile(path, []byte("old1\nold2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := NewFollower(path, true)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var mu sync.Mutex
	var lines []string

	go func() {
		_ = f.Follow(ctx, func(line string) {
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
		})
	}()

	time.Sleep(200 * time.Millisecond)

	// Append new line after follower started from end
	af, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	af.WriteString("new1\n")
	af.Close()

	time.Sleep(700 * time.Millisecond)
	cancel()

	mu.Lock()
	defer mu.Unlock()

	// Should only see the new line, not old content
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v", len(lines), lines)
	}
	if lines[0] != "new1" {
		t.Errorf("lines[0] = %q, want new1", lines[0])
	}
}

func TestFollower_ContextCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	os.WriteFile(path, []byte("x\n"), 0o644)

	f, err := NewFollower(path, true)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- f.Follow(ctx, func(string) {})
	}()

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("got err %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Follow did not return after cancel")
	}
}
