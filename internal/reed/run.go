package reed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/db"
	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

// newProcessID generates a random process ID.
func newProcessID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	h := hex.EncodeToString(b)
	return fmt.Sprintf("proc_%s_%s", h[:8], h[8:])
}

// OpenRuntime creates an Engine and stores it on the Manager.
// Manager is responsible for DB registration (INSERT + status → RUNNING).
// Caller should switch logger to per-process mode after this returns.
func (m *Manager) OpenRuntime(ctx context.Context, mode model.ProcessMode, wfSource string) (string, error) {
	if m.engineWorker == nil {
		return "", fmt.Errorf("engine not configured: WithEngine or WithWorkflow must be applied before OpenRuntime")
	}

	// 1. Generate ProcessID
	processID := newProcessID()

	// 2. DB INSERT directly as RUNNING (single atomic write, no consistency window)
	if m.db != nil {
		now := time.Now().UTC()
		row := &model.ProcessRow{
			ID:             processID,
			PID:            os.Getpid(),
			Mode:           string(mode),
			Status:         string(model.ProcessRunning),
			WorkflowSource: wfSource,
			CreatedAt:      now,
			UpdatedAt:      now,
			MetadataJSON:   "{}",
		}
		repo := db.NewProcessRepo(m.db)
		if err := repo.Insert(ctx, row); err != nil {
			return "", fmt.Errorf("register process: %w", err)
		}
	}

	// 3. Create Bus and EventSink before Engine
	m.bus = bus.New()
	if err := m.initEventLog(processID); err != nil {
		// Rollback: close bus, mark DB row as FAILED
		m.bus.Close()
		m.bus = nil
		if m.db != nil {
			repo := db.NewProcessRepo(m.db)
			_ = repo.UpdateStatus(ctx, processID, string(model.ProcessFailed), "{}")
		}
		return "", fmt.Errorf("init event log: %w", err)
	}

	// 4. Create Engine (no DB dependency)
	e, err := engine.New(m.engineWorker,
		engine.Config{
			ProcessID:      processID,
			PID:            os.Getpid(),
			Mode:           mode,
			Workflow:       m.wf,
			WorkflowSource: wfSource,
			Bus:            m.bus,
			EventSink:      m.eventLog,
		},
	)
	if err != nil {
		// Rollback: clean up resources created before engine.New
		if m.eventLog != nil {
			m.eventLog.Close()
			m.eventLog = nil
		}
		if m.bus != nil {
			m.bus.Close()
			m.bus = nil
		}
		// Rollback: mark the DB row as FAILED so it doesn't stay RUNNING forever
		if m.db != nil {
			repo := db.NewProcessRepo(m.db)
			_ = repo.UpdateStatus(ctx, processID, string(model.ProcessFailed), "{}")
		}
		return "", fmt.Errorf("open runtime: %w", err)
	}
	m.engine = e
	return processID, nil
}

// ProcessID proxies to engine.ProcessID().
func (m *Manager) ProcessID() string {
	if m.engine == nil {
		return ""
	}
	return m.engine.ProcessID()
}

// InitRuntime completes runtime initialization after process registration and logger switch.
// Handles: IPC, initial run (CLI), HTTP triggers, schedule triggers.
// Event log is already initialized in OpenRuntime (before engine creation).
func (m *Manager) InitRuntime(ctx context.Context, req *model.RunRequest, mode model.ProcessMode) error {
	processID := m.engine.ProcessID()

	// 0. Media orphan cleanup (before accepting work)
	if m.media != nil {
		if n, err := m.media.CleanOrphans(ctx); err != nil {
			log.Warn().Err(err).Msg("media orphan cleanup failed")
		} else if n > 0 {
			log.Info().Int("count", n).Msg("media orphan cleanup completed")
		}
	}

	// 1. IPC
	if err := m.startIPC(processID); err != nil {
		return fmt.Errorf("start IPC: %w", err)
	}

	// 2. Start initial run (CLI mode only)
	if mode == model.ProcessModeCLI && req != nil {
		if _, err := m.engine.Submit(req); err != nil {
			return fmt.Errorf("submit initial run: %w", err)
		}
		// CLI mode: seal after submitting so DoneCh closes when all runs complete
		m.engine.Seal()
	}

	// 3. HTTP/MCP triggers
	m.setupHTTPTriggers()

	// 4. Schedule triggers
	if err := m.setupScheduleTriggers(); err != nil {
		return err
	}

	return nil
}

// initEventLog creates the per-process event log writer.
// Called before engine.New() so the writer can be passed via Config.EventSink.
func (m *Manager) initEventLog(processID string) error {
	logDir := m.cfg.LogDir()
	w, err := engine.NewJSONLWriter(logDir, processID)
	if err != nil {
		return fmt.Errorf("init event log: %w", err)
	}
	m.eventLog = w
	return nil
}
