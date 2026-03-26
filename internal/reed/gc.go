package reed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/sjzar/reed/internal/db"
	"github.com/sjzar/reed/pkg/fsutil"
)

// ProcessLookup checks whether a process ID is known and active.
type ProcessLookup interface {
	FindByID(ctx context.Context, id string) (active bool, err error)
}

// CleanStaleSockets removes socket files for processes that are
// no longer registered or have reached a terminal status.
func CleanStaleSockets(sockDir string, lookup ProcessLookup, log zerolog.Logger) {
	entries, err := os.ReadDir(sockDir)
	if err != nil {
		return
	}
	ctx := context.Background()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "proc_") || !strings.HasSuffix(name, ".sock") {
			continue
		}
		processID := strings.TrimSuffix(name, ".sock")
		active, err := lookup.FindByID(ctx, processID)
		if err != nil {
			log.Debug().Err(err).Str("process", processID).Msg("lookup error, skipping socket")
			continue
		}
		if !active {
			sockPath := filepath.Join(sockDir, name)
			if rmErr := os.Remove(sockPath); rmErr != nil {
				log.Warn().Err(rmErr).Str("socket", sockPath).Msg("failed to remove stale socket")
			} else {
				log.Debug().Str("socket", sockPath).Msg("removed stale socket")
			}
		}
	}
}

// CleanStaleFiles removes files older than maxAge from the given directory.
func CleanStaleFiles(dir string, maxAge time.Duration, log zerolog.Logger) {
	fsutil.CleanStaleFiles(dir, maxAge, log)
}

// CleanStale performs lazy stale cleanup of sockets and old log files.
func (m *Manager) CleanStale() {
	repo, err := m.processRepo()
	if err != nil {
		return
	}
	lookup := &gcLookupAdapter{repo: repo}
	CleanStaleSockets(m.cfg.SockDir(), lookup, zerolog.Nop())
	CleanStaleFiles(m.cfg.LogDir(), 7*24*time.Hour, zerolog.Nop())
}

type gcLookupAdapter struct {
	repo *db.ProcessRepo
}

func (a *gcLookupAdapter) FindByID(ctx context.Context, id string) (bool, error) {
	row, err := a.repo.FindByID(ctx, id)
	if err != nil {
		return false, err
	}
	return isActiveStatus(row.Status), nil
}
