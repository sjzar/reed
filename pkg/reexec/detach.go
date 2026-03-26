package reexec

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	// detachFDEnv is the environment variable name for the readiness pipe fd.
	detachFDEnv = "REED_INTERNAL_DETACH_FD"

	// protocol is "ok:<processID>" or "err:<message>".
	prefixOK  = "ok:"
	prefixErr = "err:"

	detachTimeout = 30 * time.Second
)

// Pipe wraps the parent-child readiness communication channel.
// The child writes either SignalOK or SignalError; the parent reads the result.
type Pipe struct {
	file *os.File
}

// PipeFromEnv returns a Pipe if the current process was started by Detach.
// Returns nil if the env var is not set. Clears the env var to prevent propagation.
func PipeFromEnv() *Pipe {
	fdStr := os.Getenv(detachFDEnv)
	if fdStr == "" {
		return nil
	}
	os.Unsetenv(detachFDEnv)
	fd, err := strconv.Atoi(fdStr)
	if err != nil {
		return nil
	}
	f := os.NewFile(uintptr(fd), "detach-pipe")
	CloseOnExec(fd)
	return &Pipe{file: f}
}

// SignalOK tells the parent that the child started successfully.
func (p *Pipe) SignalOK(processID string) {
	if p == nil || p.file == nil {
		return
	}
	fmt.Fprintf(p.file, "%s%s\n", prefixOK, processID)
	p.file.Close()
	p.file = nil
}

// SignalError tells the parent that the child failed to start.
func (p *Pipe) SignalError(err error) {
	if p == nil || p.file == nil {
		return
	}
	msg := strings.ReplaceAll(err.Error(), "\n", " ")
	fmt.Fprintf(p.file, "%s%s\n", prefixErr, msg)
	p.file.Close()
	p.file = nil
}

// Close releases the pipe without signaling. Use when the pipe is no longer needed.
func (p *Pipe) Close() {
	if p == nil || p.file == nil {
		return
	}
	p.file.Close()
	p.file = nil
}

// Detach re-executes the current process in the background, passing a readiness
// pipe via fd 3. Returns the child PID and process ID on success.
func Detach(argv []string) (pid int, processID string, err error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return 0, "", fmt.Errorf("detach: create pipe: %w", err)
	}

	child := exec.Command(argv[0], argv[1:]...)
	child.Env = append(os.Environ(), detachFDEnv+"=3")
	child.ExtraFiles = []*os.File{pw} // ExtraFiles[0] → fd 3
	child.Stdout = nil
	child.Stderr = nil
	child.Stdin = nil
	if err := child.Start(); err != nil {
		pr.Close()
		pw.Close()
		return 0, "", fmt.Errorf("detach: %w", err)
	}
	pw.Close()

	pid = child.Process.Pid
	_ = child.Process.Release()

	// Read readiness signal with timeout.
	type readResult struct {
		line string
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		scanner := bufio.NewScanner(pr)
		if scanner.Scan() {
			ch <- readResult{line: scanner.Text()}
		} else {
			ch <- readResult{err: scanner.Err()}
		}
	}()

	var line string
	select {
	case res := <-ch:
		pr.Close()
		if res.err != nil {
			return pid, "", fmt.Errorf("detach: read pipe: %w", res.err)
		}
		if res.line == "" {
			return pid, "", fmt.Errorf("detach: child exited without signaling readiness")
		}
		line = res.line
	case <-time.After(detachTimeout):
		pr.Close()
		return pid, "", fmt.Errorf("detach: timed out waiting for child readiness (30s)")
	}

	if after, ok := strings.CutPrefix(line, prefixOK); ok {
		return pid, after, nil
	}
	if after, ok := strings.CutPrefix(line, prefixErr); ok {
		return pid, "", fmt.Errorf("detach: %s", after)
	}
	return pid, "", fmt.Errorf("detach: unexpected response from child: %s", line)
}
