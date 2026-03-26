package logutil

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const pollInterval = 500 * time.Millisecond

// Follower implements tail -f semantics for a file.
// It uses fsnotify for immediate notification with a poll fallback.
type Follower struct {
	path    string
	file    *os.File
	offset  int64
	watcher *fsnotify.Watcher
}

// NewFollower opens a file for tailing from the current end.
// If startFromEnd is true, seeks to EOF before reading.
func NewFollower(path string, startFromEnd bool) (*Follower, error) {
	return newFollower(path, startFromEnd)
}

// NewFollowerWait is like NewFollower but waits for the file to appear
// if it does not exist yet, polling until ctx is cancelled.
func NewFollowerWait(ctx context.Context, path string, startFromEnd bool) (*Follower, error) {
	// Fast path: file already exists.
	fl, err := newFollower(path, startFromEnd)
	if err == nil {
		return fl, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	// Watch the parent directory for file creation.
	dir := filepath.Dir(path)
	w, _ := fsnotify.NewWatcher()
	if w != nil {
		_ = w.Add(dir)
		defer w.Close()
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case _, ok := <-wEvents(w):
			if ok {
				fl, err := newFollower(path, startFromEnd)
				if err == nil {
					return fl, nil
				}
				if !os.IsNotExist(err) {
					return nil, err
				}
			}
		case <-ticker.C:
			fl, err := newFollower(path, startFromEnd)
			if err == nil {
				return fl, nil
			}
			if !os.IsNotExist(err) {
				return nil, err
			}
		}
	}
}

func wEvents(w *fsnotify.Watcher) <-chan fsnotify.Event {
	if w != nil {
		return w.Events
	}
	return nil
}

func newFollower(path string, startFromEnd bool) (*Follower, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	var offset int64
	if startFromEnd {
		offset, err = f.Seek(0, io.SeekEnd)
		if err != nil {
			f.Close()
			return nil, err
		}
	}

	w, _ := fsnotify.NewWatcher()
	if w != nil {
		_ = w.Add(path)
	}

	return &Follower{
		path:    path,
		file:    f,
		offset:  offset,
		watcher: w,
	}, nil
}

// Follow reads new lines as they are appended and sends them to the callback.
// Blocks until ctx is cancelled. Uses fsnotify when available, falls back to polling.
func (f *Follower) Follow(ctx context.Context, fn func(line string)) error {
	defer f.Close()

	// Read any existing content from current offset
	f.readLines(fn)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-f.events():
			if !ok {
				// watcher closed, fall back to poll only
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
					f.readLines(fn)
				}
				continue
			}
			f.readLines(fn)
		case <-ticker.C:
			f.readLines(fn)
		}
	}
}

// readLines reads all available complete lines from the current offset.
func (f *Follower) readLines(fn func(string)) {
	// Seek to our tracked offset
	if _, err := f.file.Seek(f.offset, io.SeekStart); err != nil {
		return
	}

	scanner := bufio.NewScanner(f.file)
	for scanner.Scan() {
		line := scanner.Text()
		fn(line)
	}

	// Update offset to current position
	pos, err := f.file.Seek(0, io.SeekCurrent)
	if err == nil {
		f.offset = pos
	}
}

// events returns the fsnotify event channel, or a nil channel if no watcher.
func (f *Follower) events() <-chan fsnotify.Event {
	if f.watcher != nil {
		return f.watcher.Events
	}
	return nil
}

// Close releases all resources.
func (f *Follower) Close() error {
	if f.watcher != nil {
		f.watcher.Close()
	}
	return f.file.Close()
}
