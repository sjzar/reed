package reed

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/sjzar/reed/pkg/logutil"
)

// LogReadOpts holds the parameters for log reading.
type LogReadOpts struct {
	Target         string
	Follow         bool
	IncludeProcess bool
	TailN          int
	Writer         io.Writer
}

// ReadLogs resolves the target, builds log paths, and dumps or follows.
func (m *Manager) ReadLogs(ctx context.Context, opts LogReadOpts) error {
	repo, err := m.processRepo()
	if err != nil {
		return err
	}
	row, err := resolveTarget(ctx, repo, opts.Target)
	if err != nil {
		return err
	}

	paths := []string{m.cfg.EventLogPath(row.ID)}
	if opts.IncludeProcess {
		paths = append(paths, m.cfg.ProcessLogPath(row.ID))
	}

	w := opts.Writer
	if w == nil {
		w = os.Stdout
	}

	if opts.Follow {
		return followLogs(ctx, paths, w)
	}
	return dumpLogs(paths, opts.TailN, w)
}

func dumpLogs(paths []string, tailN int, w io.Writer) error {
	var allLines []string
	for _, p := range paths {
		lines, err := readAllLines(p)
		if err != nil {
			continue
		}
		allLines = append(allLines, lines...)
	}
	if tailN > 0 && len(allLines) > tailN {
		allLines = allLines[len(allLines)-tailN:]
	}
	for _, line := range allLines {
		fmt.Fprintln(w, line)
	}
	return nil
}

func readAllLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func followLogs(ctx context.Context, paths []string, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	var mu sync.Mutex
	done := make(chan error, len(paths))
	for _, p := range paths {
		go func() {
			f, err := logutil.NewFollowerWait(ctx, p, false)
			if err != nil {
				done <- nil
				return
			}
			done <- f.Follow(ctx, func(line string) {
				mu.Lock()
				fmt.Fprintln(w, line)
				mu.Unlock()
			})
		}()
	}
	for range paths {
		<-done
	}
	return nil
}
