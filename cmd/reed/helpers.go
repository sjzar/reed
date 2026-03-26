package reed

import (
	"fmt"

	"github.com/spf13/cobra"

	reedmgr "github.com/sjzar/reed/internal/reed"
)

// withDBManager handles the common bootstrap pattern for short commands
// (ps, status, stop, logs): load config → init logger → open DB → run → shutdown.
func withDBManager(cmd *cobra.Command, fn func(m *reedmgr.Manager) error) error {
	cfg := loadConfig(cmd)
	cleanup := initLogger(cfg, LogModeShared, "")
	defer cleanup()

	m, err := reedmgr.New(cfg, reedmgr.WithDB())
	if err != nil {
		return fmt.Errorf("cannot open Reed database: %w; check that REED_HOME or --home is set correctly", err)
	}
	defer m.Shutdown()

	return fn(m)
}
