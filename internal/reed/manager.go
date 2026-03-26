package reed

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/db"
	"github.com/sjzar/reed/internal/engine"
	reedhttp "github.com/sjzar/reed/internal/http"
	"github.com/sjzar/reed/internal/mcp"
	"github.com/sjzar/reed/internal/media"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/security"
	"github.com/sjzar/reed/internal/session"
	"github.com/sjzar/reed/internal/worker"
	"github.com/sjzar/reed/pkg/cron"
)

// Option configures which subsystems the Manager bootstraps.
type Option func(*Manager) error

// RunResolver resolves trigger params into a RunRequest.
type RunResolver interface {
	ResolveRunRequest(ctx context.Context, wf *model.Workflow, wfSource string, params model.TriggerParams) (*model.RunRequest, error)
}

// Manager is the lifecycle facade for a reed process.
// It wires together services based on the command being executed.
type Manager struct {
	cfg          *conf.Config
	db           *db.DB
	bus          *bus.Bus
	engine       *engine.Engine
	engineWorker engine.Worker // set by WithEngine/WithWorkflow, used to create Engine later
	http         *reedhttp.Service
	httpAddr     bool // true when HTTP TCP listener should start (service mode)
	mcpPool      *mcp.Pool
	sessionSvc   *session.Service
	scheduler    *cron.Scheduler
	secretStore  *security.SecretStore
	ipcReady     bool
	eventLog     *engine.JSONLWriter

	// Runtime context for InitRuntime
	wf       *model.Workflow
	wfSource string
	workDir  string // project CWD at startup; fallback for steps without explicit workdir
	resolver RunResolver

	signaler     signaler // defaults to osSignaler{} in New()
	started      bool     // set by Start(); prevents double-start in Run()
	shutdownOnce sync.Once
	shutdownErr  error
	media        *media.LocalService
}

// Bus returns the event bus, or nil if not initialized.
func (m *Manager) Bus() *bus.Bus { return m.bus }

// New creates a Manager with the given options.
func New(cfg *conf.Config, opts ...Option) (*Manager, error) {
	m := &Manager{cfg: cfg, signaler: osSignaler{}}
	for _, opt := range opts {
		if err := opt(m); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// WithDB bootstraps the SQLite database.
func WithDB() Option {
	return func(m *Manager) error {
		d, err := db.Open(m.cfg.DBDir())
		if err != nil {
			return err
		}
		m.db = d
		return nil
	}
}

// WithHTTP bootstraps the HTTP server on the given port.
func WithHTTP(port int) Option {
	return func(m *Manager) error {
		addr := fmt.Sprintf("0.0.0.0:%d", port)
		m.http = reedhttp.New(reedhttp.Config{
			Debug: m.cfg.Debug,
			Addr:  addr,
		})
		m.httpAddr = true
		return nil
	}
}

// WithEngine bootstraps the engine worker.
// Actual engine.New() is called in OpenRuntime when mode/workflow are known.
func WithEngine() Option {
	return func(m *Manager) error {
		if m.db == nil {
			return fmt.Errorf("WithDB must be applied before WithEngine")
		}
		m.engineWorker = worker.NewRouter()
		return nil
	}
}

// WithMedia bootstraps the media service (local file storage + SQLite metadata).
// Must be applied after WithDB.
func WithMedia() Option {
	return func(m *Manager) error {
		if m.db == nil {
			return fmt.Errorf("WithDB must be applied before WithMedia")
		}
		repo := db.NewMediaRepo(m.db)
		ttl := m.cfg.Media.ParseTTL()
		m.media = media.NewLocalService(repo, m.cfg.MediaDir(), ttl)
		return nil
	}
}

// WithWorkflow bootstraps the MCP pool and engine with a workflow-aware worker router.
// This replaces WithEngine when running a workflow that may have agents and MCP servers.
// workDir is the project CWD at startup, used for skill scanning.
func WithWorkflow(wf *model.Workflow, workDir string) Option {
	return func(m *Manager) error {
		if wf == nil {
			return fmt.Errorf("workflow must not be nil")
		}
		if m.db == nil {
			return fmt.Errorf("WithDB must be applied before WithWorkflow")
		}

		m.wf = wf
		m.wfSource = wf.Source
		m.workDir = workDir

		routeStore := db.NewSessionRouteRepo(m.db)
		result, err := Build(context.Background(), BuildConfig{
			Workflow:    wf,
			WorkDir:     workDir,
			Models:      m.cfg.Models,
			RouteStore:  routeStore,
			SessionDir:  m.cfg.SessionDir(),
			HomeDir:     m.cfg.Home,
			SkillModDir: m.cfg.SkillModDir(),
			MemoryDir:   m.cfg.MemoryDir(),
			Media:       m.media, // may be nil if WithMedia not applied
		})
		if err != nil {
			return err
		}

		m.engineWorker = result.Router
		m.mcpPool = result.Pool
		m.sessionSvc = result.SessionSvc
		return nil
	}
}

// WithIPC marks IPC as ready; actual start happens via StartIPC after process registration.
// If HTTP is not bootstrapped, IPC creates its own minimal HTTP service for the UDS listener.
func WithIPC() Option {
	return func(m *Manager) error {
		m.ipcReady = true
		return nil
	}
}

// WithResolver sets the RunResolver used by HTTP/schedule triggers in InitRuntime.
func WithResolver(r RunResolver) Option {
	return func(m *Manager) error {
		m.resolver = r
		return nil
	}
}

// WithSecrets stores a pre-created SecretStore on the Manager.
func WithSecrets(store *security.SecretStore) Option {
	return func(m *Manager) error {
		m.secretStore = store
		return nil
	}
}

// Resolver returns the pre-composed resolver chain.
func (m *Manager) Resolver() RunResolver { return m.resolver }

// SecretStore returns the secret store, or nil if not initialized.
func (m *Manager) SecretStore() *security.SecretStore { return m.secretStore }

// MediaUploader is the narrow interface for uploading media from the CLI/HTTP layer.
type MediaUploader interface {
	Upload(ctx context.Context, r io.Reader, mimeType string) (*model.MediaEntry, error)
}

// MediaUploader returns the media uploader, or nil if media is not initialized.
func (m *Manager) MediaUploader() MediaUploader {
	if m.media == nil {
		return nil
	}
	return m.media
}

// processRepo returns a ProcessRepo, or an error if the DB is not initialized.
func (m *Manager) processRepo() (*db.ProcessRepo, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not initialized: WithDB must be applied first")
	}
	return db.NewProcessRepo(m.db), nil
}
