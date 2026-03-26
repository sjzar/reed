package reed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sjzar/reed/internal/db"
	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

const graceTimeout = 3 * time.Second

// Start binds the HTTP port (if configured) and starts the scheduler.
// Returns an error immediately if the port is unavailable.
// Idempotent: subsequent calls are no-ops.
func (m *Manager) Start() error {
	if m.started {
		return nil
	}
	if m.http != nil && m.httpAddr {
		if err := m.http.Listen(); err != nil {
			return err
		}
	}
	if m.scheduler != nil {
		m.scheduler.Start()
	}
	m.started = true
	return nil
}

// Run starts all bootstrapped subsystems and blocks until shutdown.
// Handles SIGTERM/SIGINT → Context Cancel → 3s grace → os.Exit(1).
func (m *Manager) Run() error {
	// Start binds HTTP port and starts scheduler (idempotent if already called).
	if err := m.Start(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start HTTP accept loop only if a listener was bound.
	errCh := make(chan error, 1)
	if m.http != nil && m.httpAddr {
		go func() {
			if err := m.http.Serve(); err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		}()
	}

	// Wait for signal, error, or runtime completion
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	var doneCh <-chan struct{}
	if m.engine != nil {
		doneCh = m.engine.DoneCh()
	}

	select {
	case err := <-errCh:
		log.Error().Err(err).Msg("server error")
		cancel()
		return err
	case sig := <-quit:
		log.Info().Msgf("received signal: %s", sig)
		cancel()
		if m.engine != nil {
			m.engine.StopAll()
		}
	case <-doneCh:
		log.Info().Msg("all runs completed")
		cancel()
	case <-ctx.Done():
	}

	if err := m.Shutdown(); err != nil {
		return err
	}

	// Return non-zero exit semantics if any run failed
	if m.engine != nil && m.engine.HasFailedRuns() {
		return fmt.Errorf("one or more runs failed")
	}
	return nil
}

// Snapshot returns a read-only snapshot of the runtime state.
// Returns an empty ProcessView if the runtime is not initialized.
func (m *Manager) Snapshot() engine.ProcessView {
	if m.engine == nil {
		return engine.ProcessView{}
	}
	return m.engine.Snapshot()
}

// Shutdown performs ordered teardown:
// 1. Stop engine (cancel runs, wait workers)
// 2. Stop MCP pool (close all server connections)
// 3. Stop IPC (UDS listener)
// 4. Stop HTTP (TCP listener, only if started)
// 5. Close DB
// 6. Close event log
func (m *Manager) Shutdown() error {
	m.shutdownOnce.Do(m.doShutdown)
	return m.shutdownErr
}

func (m *Manager) doShutdown() {
	var errs []error
	ctx, cancel := context.WithTimeout(context.Background(), graceTimeout)
	defer cancel()

	if m.scheduler != nil {
		m.scheduler.Stop()
	}

	if m.engine != nil {
		if err := m.engine.Close(ctx); err != nil {
			log.Error().Err(err).Msg("runtime close error")
			errs = append(errs, fmt.Errorf("engine close: %w", err))
		}
		if m.db != nil {
			finalStatus := m.deriveFinalProcessStatus()
			dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer dbCancel()
			repo := db.NewProcessRepo(m.db)
			if err := repo.UpdateStatus(dbCtx, m.engine.ProcessID(), string(finalStatus), "{}"); err != nil {
				log.Error().Err(err).Msg("update process status error")
				errs = append(errs, fmt.Errorf("update process status: %w", err))
			}
		}
	}

	if m.mcpPool != nil {
		m.mcpPool.StopAll()
	}

	if m.http != nil {
		ipcCtx, ipcCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := m.http.ShutdownIPC(ipcCtx); err != nil {
			log.Error().Err(err).Msg("IPC shutdown error")
			errs = append(errs, fmt.Errorf("IPC shutdown: %w", err))
		}
		ipcCancel()
		if m.httpAddr {
			httpCtx, httpCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := m.http.Shutdown(httpCtx); err != nil {
				log.Error().Err(err).Msg("HTTP shutdown error")
				errs = append(errs, fmt.Errorf("HTTP shutdown: %w", err))
			}
			httpCancel()
		}
	}

	if m.bus != nil {
		m.bus.Close()
	}

	if m.sessionSvc != nil {
		if err := m.sessionSvc.Close(); err != nil {
			log.Error().Err(err).Msg("session service close error")
		}
	}

	if m.media != nil {
		if n, err := m.media.GC(context.Background()); err != nil {
			log.Warn().Err(err).Msg("media GC failed")
		} else if n > 0 {
			log.Info().Int("count", n).Msg("media GC completed")
		}
	}

	if m.db != nil {
		if err := m.db.Close(); err != nil {
			log.Error().Err(err).Msg("DB close error")
			errs = append(errs, fmt.Errorf("DB close: %w", err))
		}
	}

	if m.eventLog != nil {
		if err := m.eventLog.Close(); err != nil {
			log.Error().Err(err).Msg("event log close error")
			errs = append(errs, fmt.Errorf("event log close: %w", err))
		}
	}

	m.shutdownErr = errors.Join(errs...)
	if m.shutdownErr != nil {
		log.Warn().Err(m.shutdownErr).Msg("reed exited with errors")
	} else {
		log.Info().Msg("reed exited gracefully")
	}
}

// deriveFinalProcessStatus determines the final DB status for the process.
// Only RunFailed maps to ProcessFailed. RunCanceled maps to ProcessStopped
// (canceled is a normal stop outcome, not a failure).
func (m *Manager) deriveFinalProcessStatus() model.ProcessStatus {
	if m.engine == nil {
		return model.ProcessStopped
	}
	snap := m.engine.Snapshot()
	for _, rv := range snap.TerminalRuns {
		if rv.Status == model.RunFailed {
			return model.ProcessFailed
		}
	}
	return model.ProcessStopped
}
