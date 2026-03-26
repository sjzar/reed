package worker

import (
	"bytes"
)

// maxOutputBytes is the maximum number of bytes a worker will capture in memory.
// Beyond this limit, additional output is silently discarded.
const maxOutputBytes = 10 << 20 // 10 MB

// limitedWriter wraps a bytes.Buffer with a capacity limit.
// Write always returns len(p), nil — even when truncating — so exec.Cmd
// never sees a short-write error.
type limitedWriter struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func newLimitedWriter(limit int) *limitedWriter {
	return &limitedWriter{limit: limit}
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.buf.Len() >= w.limit {
		w.overflow = true
		return len(p), nil
	}
	remaining := w.limit - w.buf.Len()
	if len(p) > remaining {
		w.buf.Write(p[:remaining])
		w.overflow = true
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

func (w *limitedWriter) String() string   { return w.buf.String() }
func (w *limitedWriter) Bytes() []byte    { return w.buf.Bytes() }
func (w *limitedWriter) Overflowed() bool { return w.overflow }
