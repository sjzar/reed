package reed

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/pkg/logutil"
)

// LogMode determines where zerolog output is directed.
type LogMode int

const (
	// LogModeShared writes to reed.log (short commands: ps, status, stop, validate).
	// Debug mode also writes to stderr with ConsoleWriter.
	LogModeShared LogMode = iota
	// LogModeService writes to stdout + proc_<id>.log (foreground long-running).
	LogModeService
	// LogModeCLI writes to proc_<id>.log only (foreground interactive workflow).
	LogModeCLI
	// LogModeDetach writes to proc_<id>.log only (background workflow).
	LogModeDetach
)

// initLogger configures the global zerolog logger based on mode.
// Returns a cleanup function that closes any file writers.
func initLogger(cfg *conf.Config, mode LogMode, processID string) func() {
	zerolog.TimeFieldFormat = time.RFC3339

	if cfg.Debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	var writers []io.Writer
	var closers []io.Closer

	switch mode {
	case LogModeShared:
		rw := logutil.NewRotatingWriter(filepath.Join(cfg.LogDir(), "reed.log"))
		closers = append(closers, rw)
		writers = append(writers, rw)
		if cfg.Debug {
			writers = append(writers, zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
		}

	case LogModeService:
		procLog := logutil.NewRotatingWriter(cfg.ProcessLogPath(processID))
		closers = append(closers, procLog)
		writers = append(writers, os.Stdout, procLog)

	case LogModeCLI, LogModeDetach:
		procLog := logutil.NewRotatingWriter(cfg.ProcessLogPath(processID))
		closers = append(closers, procLog)
		writers = append(writers, procLog)
	}

	if len(writers) == 0 {
		writers = append(writers, os.Stdout)
	}

	var w io.Writer
	if len(writers) == 1 {
		w = writers[0]
	} else {
		w = zerolog.MultiLevelWriter(writers...)
	}

	log.Logger = zerolog.New(w).With().Timestamp().Logger()

	return func() {
		for _, c := range closers {
			c.Close()
		}
	}
}
