package http

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/sjzar/reed/internal/model"
)

// Config holds the HTTP-specific configuration needed by this package.
type Config struct {
	Debug bool
	Addr  string
}

// StatusProvider supplies live process status for /v1/ping and /v1/status.
type StatusProvider interface {
	PingData() model.PingResponse
	StatusData() (any, error)
	RunData(runID string) (any, bool)
	StopRun(runID string) bool
}

// MediaOpener is the narrow interface for serving media files.
type MediaOpener interface {
	Open(ctx context.Context, id string) (io.ReadCloser, *model.MediaEntry, error)
}

type Service struct {
	cfg      Config
	router   *gin.Engine
	server   *http.Server
	ln       net.Listener // pre-bound TCP listener (set by Listen)
	ipcSrv   *http.Server
	ipcLn    net.Listener
	provider StatusProvider
	media    MediaOpener
}

func New(cfg Config) *Service {
	s := &Service{
		cfg:    cfg,
		router: initGin(cfg.Debug),
	}
	s.initRouter()
	return s
}

// SetStatusProvider sets the provider used by /v1/ping and /v1/status handlers.
func (s *Service) SetStatusProvider(p StatusProvider) {
	s.provider = p
}

func initGin(debug bool) *gin.Engine {
	if debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	router.TrustedPlatform = gin.PlatformCloudflare
	router.ForwardedByClientIP = true
	router.RemoteIPHeaders = []string{"X-Forwarded-For", "X-Real-IP"}
	if err := router.SetTrustedProxies([]string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	}); err != nil {
		log.Err(err).Msg("failed to set trusted proxies")
	}

	router.Use(
		RecoveryMiddleware(),
		ErrorHandlerMiddleware(),
		gin.LoggerWithWriter(log.Logger, "/health"),
		cors.New(cors.Config{
			AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
			AllowHeaders: []string{"Origin", "Content-Length", "Content-Type"},
			// Intentional: Reed is local-first and binds to localhost. Restricting
			// origins would break local dev tooling.
			AllowAllOrigins: true,
			MaxAge:          12 * time.Hour,
		}),
	)

	return router
}

// Listen binds the TCP port without accepting connections.
// Call Serve afterward to start accepting. Idempotent: no-op if already bound.
func (s *Service) Listen() error {
	if s.ln != nil {
		return nil
	}
	addr := s.cfg.Addr
	if addr == "" {
		return fmt.Errorf("HTTP addr not configured")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.ln = ln
	return nil
}

// Serve accepts connections on the listener opened by Listen.
// Blocks until the server is shut down.
func (s *Service) Serve() error {
	if s.ln == nil {
		return fmt.Errorf("Listen must be called before Serve")
	}
	s.server = &http.Server{
		Handler: s.router,
	}
	log.Info().Msgf("starting HTTP server on %s", s.ln.Addr())
	return s.server.Serve(s.ln)
}

// ListenAndServe binds the port and accepts connections (blocking).
func (s *Service) ListenAndServe() error {
	if err := s.Listen(); err != nil {
		return err
	}
	return s.Serve()
}

// TriggerDispatcher handles incoming HTTP trigger requests.
type TriggerDispatcher interface {
	HandleHTTPTrigger(route model.HTTPRoute, c *gin.Context)
}

// RegisterWorkflowRoutes registers workflow-defined HTTP routes on the router.
func (s *Service) RegisterWorkflowRoutes(routes []model.HTTPRoute, dispatcher TriggerDispatcher) {
	for _, r := range routes {
		route := r
		method := strings.ToUpper(route.Method)
		if method == "" {
			method = "POST"
		}
		s.router.Handle(method, route.Path, func(c *gin.Context) {
			dispatcher.HandleHTTPTrigger(route, c)
		})
	}
}

// StartIPC starts the UDS listener on the same gin router.
func (s *Service) StartIPC(sockPath string) error {
	// Remove stale socket file if it exists
	_ = os.Remove(sockPath)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		return fmt.Errorf("create sock dir: %w", err)
	}

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", sockPath, err)
	}
	_ = os.Chmod(sockPath, 0o600)

	s.ipcLn = ln
	s.ipcSrv = &http.Server{
		Handler:     s.router,
		ConnContext: ipcConnContext,
	}

	go func() {
		if err := s.ipcSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("IPC serve error")
		}
	}()
	log.Info().Str("sock", sockPath).Msg("IPC listening")
	return nil
}

// ShutdownIPC gracefully stops the IPC server and removes the socket file.
func (s *Service) ShutdownIPC(ctx context.Context) error {
	if s.ipcSrv == nil {
		return nil
	}
	err := s.ipcSrv.Shutdown(ctx)
	if s.ipcLn != nil {
		// Remove socket file; ipcLn.Addr() holds the path
		_ = os.Remove(s.ipcLn.Addr().String())
	}
	return err
}

func (s *Service) Shutdown(ctx context.Context) error {
	if s.server == nil {
		// Server was never started (Start without Run), close pre-bound listener.
		if s.ln != nil {
			return s.ln.Close()
		}
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *Service) Engine() *gin.Engine {
	return s.router
}

// RegisterMCPHandler mounts an MCP Streamable HTTP handler at /mcp.
func (s *Service) RegisterMCPHandler(handler http.Handler) {
	s.router.Any("/mcp", gin.WrapH(handler))
}

// SetMediaService sets the media opener used by /v1/media/:id.
func (s *Service) SetMediaService(m MediaOpener) {
	s.media = m
}
