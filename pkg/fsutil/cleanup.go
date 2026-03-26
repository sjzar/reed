package fsutil

import (
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
)

// CleanStaleFiles removes files older than maxAge from the given directory.
// Subdirectories are skipped. Errors reading individual entries are ignored.
func CleanStaleFiles(dir string, maxAge time.Duration, log zerolog.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			fp := filepath.Join(dir, e.Name())
			if rmErr := os.Remove(fp); rmErr == nil {
				log.Debug().Str("file", fp).Msg("removed stale file")
			}
		}
	}
}
