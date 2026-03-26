package logutil

import (
	"io"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Default rotation settings.
const (
	DefaultMaxSizeMB  = 50
	DefaultMaxAgeDays = 30
	DefaultMaxBackups = 3
)

// RotatingOption configures a rotating writer.
type RotatingOption func(*lumberjack.Logger)

// WithMaxSize sets the max file size in MB before rotation.
func WithMaxSize(mb int) RotatingOption {
	return func(l *lumberjack.Logger) { l.MaxSize = mb }
}

// WithMaxAge sets the max age in days before old files are deleted.
func WithMaxAge(days int) RotatingOption {
	return func(l *lumberjack.Logger) { l.MaxAge = days }
}

// WithMaxBackups sets the max number of old log files to retain.
func WithMaxBackups(n int) RotatingOption {
	return func(l *lumberjack.Logger) { l.MaxBackups = n }
}

// WithCompress enables gzip compression of rotated files.
func WithCompress(on bool) RotatingOption {
	return func(l *lumberjack.Logger) { l.Compress = on }
}

// NewRotatingWriter returns an io.WriteCloser that auto-rotates.
func NewRotatingWriter(path string, opts ...RotatingOption) io.WriteCloser {
	l := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    DefaultMaxSizeMB,
		MaxAge:     DefaultMaxAgeDays,
		MaxBackups: DefaultMaxBackups,
		Compress:   true,
	}
	for _, o := range opts {
		o(l)
	}
	return l
}
