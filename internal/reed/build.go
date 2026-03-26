package reed

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/sjzar/reed/internal/agent"
	"github.com/sjzar/reed/internal/ai"
	"github.com/sjzar/reed/internal/ai/middleware"
	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/mcp"
	"github.com/sjzar/reed/internal/media"
	"github.com/sjzar/reed/internal/memory"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/session"
	"github.com/sjzar/reed/internal/skill"
	"github.com/sjzar/reed/internal/tool"
	"github.com/sjzar/reed/internal/worker"
	"github.com/sjzar/reed/internal/workflow"
)

// BuildConfig is the full set of inputs for building a Worker dependency chain.
type BuildConfig struct {
	Workflow    *model.Workflow
	WorkDir     string
	Models      conf.ModelsConfig
	RouteStore  session.RouteStore
	SessionDir  string
	HomeDir     string // ~/.reed/ — used for skill manifests
	SkillModDir string
	MemoryDir   string
	Media       *media.LocalService // nil = no media support
}

// BuildResult contains the constructed Worker and resources that need lifecycle management.
type BuildResult struct {
	Router     *worker.Router   // final Worker implementation
	Pool       *mcp.Pool        // MCP pool; Manager must call Pool.StopAll() on shutdown
	SessionSvc *session.Service // session service; caller must Close() to release JSONL writers
}

// Close cleans up resources held by BuildResult.
func (r *BuildResult) Close() {
	if r == nil {
		return
	}
	if r.SessionSvc != nil {
		_ = r.SessionSvc.Close()
	}
	if r.Pool != nil {
		r.Pool.StopAll()
	}
}

// Build constructs the full Worker dependency chain from a workflow configuration.
func Build(ctx context.Context, cfg BuildConfig) (*BuildResult, error) {
	if cfg.Workflow == nil {
		return nil, fmt.Errorf("workflow must not be nil")
	}

	// MCP pool with cancellable context so async init goroutines are
	// stopped promptly if a later step fails.
	pool := mcp.NewPool(mcp.WithTransportFactory(mcp.NewTransportFactory()))
	initCtx, cancelInit := context.WithCancel(ctx)
	ok := false
	defer func() {
		if !ok {
			cancelInit()
			pool.StopAll()
		}
	}()
	if len(cfg.Workflow.MCPServers) > 0 {
		pool.LoadAndInit(initCtx, cfg.Workflow.MCPServers)
	}

	aiService, sessionSvc, err := newAIServices(cfg.Models, cfg.SessionDir, cfg.RouteStore)
	if err != nil {
		return nil, err
	}

	// Tool registry + MCP tools
	mcpTools, err := tool.WrapMCPTools(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("wrap mcp tools: %w", err)
	}
	reg, toolSvc, err := newTooling(sessionSvc, mcpTools)
	if err != nil {
		return nil, err
	}

	// Skill service: scan installed + load workflow skills
	skillSvc := skill.New(cfg.HomeDir, cfg.SkillModDir)
	if err := skillSvc.ScanInstalled(ctx, cfg.WorkDir); err != nil {
		return nil, fmt.Errorf("scan installed skills: %w", err)
	}
	workflowDir := filepath.Dir(cfg.Workflow.Source)
	if err := skillSvc.LoadWorkflow(ctx, cfg.Workflow.Skills, workflowDir); err != nil {
		return nil, fmt.Errorf("load workflow skills: %w", err)
	}
	if err := workflow.ValidateSkillRefsResolvable(cfg.Workflow, skillSvc.AllIDs()); err != nil {
		return nil, fmt.Errorf("validate skill refs: %w", err)
	}

	agentRunner, err := newAgent(ctx, agentEngineDeps{
		aiService:  aiService,
		sessionSvc: sessionSvc,
		toolSvc:    toolSvc,
		reg:        reg,
		skillSvc:   skillSvc,
		models:     cfg.Models,
		memoryDir:  cfg.MemoryDir,
		media:      cfg.Media,
	})
	if err != nil {
		return nil, err
	}

	// Factory + router
	factory := worker.NewFactory(cfg.Workflow, agentRunner, reg.CoreToolIDs(), skillSvc)
	router := factory.NewRouter()

	ok = true
	cancelInit() // no longer needed; pool is now caller's responsibility
	return &BuildResult{
		Router:     router,
		Pool:       pool,
		SessionSvc: sessionSvc,
	}, nil
}

// --- Shared build helpers ---

// newAIServices creates the AI service and session service.
func newAIServices(models conf.ModelsConfig, sessionDir string, routeStore session.RouteStore) (*ai.Service, *session.Service, error) {
	aiService, err := ai.New(models)
	if err != nil {
		return nil, nil, fmt.Errorf("create ai service: %w", err)
	}
	sessionSvc := session.New(sessionDir, routeStore, nil, nil, session.WithInbox(sessionDir))
	return aiService, sessionSvc, nil
}

// newTooling creates a tool registry and service, registering any MCP tools provided.
func newTooling(sessionSvc *session.Service, mcpTools []tool.Tool) (*tool.Registry, *tool.Service, error) {
	reg := tool.NewRegistry()
	for _, t := range mcpTools {
		if err := reg.RegisterWithGroup(t, tool.GroupMCP); err != nil {
			return nil, nil, fmt.Errorf("register mcp tool %s: %w", t.Def().Name, err)
		}
	}
	toolSvc := tool.NewService(reg, tool.WithSession(sessionSvc))
	return reg, toolSvc, nil
}

// agentEngineDeps groups the dependencies for creating an agent engine.
type agentEngineDeps struct {
	aiService  *ai.Service
	sessionSvc *session.Service
	toolSvc    *tool.Service
	reg        *tool.Registry
	skillSvc   *skill.Service
	models     conf.ModelsConfig
	memoryDir  string
	media      *media.LocalService // nil = no media support
}

// newAgent creates the agent runner with memory, subagent runner, and builtin tools.
func newAgent(_ context.Context, deps agentEngineDeps) (*agent.Runner, error) {
	// Memory
	memExtractor := ai.NewMemoryExtractor(deps.aiService.Responses, deps.models.Default)
	memProvider := memory.NewFileProvider(deps.memoryDir, memExtractor)

	// Agent engine — optional media deflation
	var agentOpts []agent.Option
	if deps.media != nil {
		agentOpts = append(agentOpts, agent.WithMedia(deps.media))
	}

	// Wrap AI service with media resolver middleware if media is available.
	var agentAI agent.AIService = deps.aiService
	if deps.media != nil {
		agentAI = wrapWithMediaMiddleware(deps.aiService, deps.media)
	}
	agentEngine := agent.New(agentAI, deps.sessionSvc, agent.WrapToolService(deps.toolSvc), deps.skillSvc, memProvider, agentOpts...)

	// SubAgent runner + builtin tools
	subRunner := agent.NewSubAgentRunner(agentEngine, agent.SubAgentRunConfig{})
	if err := tool.RegisterBuiltins(deps.reg, tool.WithSubAgentRunner(subRunner)); err != nil {
		return nil, fmt.Errorf("register builtin tools: %w", err)
	}

	return agentEngine, nil
}

// mediaAIService wraps an AI service with media resolution middleware.
type mediaAIService struct {
	original *ai.Service
	wrapped  *middleware.Media
}

func (s *mediaAIService) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	return s.wrapped.Responses(ctx, req)
}

func (s *mediaAIService) ModelMetadataFor(modelRef string) model.ModelMetadata {
	return s.original.ModelMetadataFor(modelRef)
}

func wrapWithMediaMiddleware(svc *ai.Service, mediaSvc *media.LocalService) *mediaAIService {
	wrapped := middleware.NewMedia(svc, mediaSvc)
	return &mediaAIService{original: svc, wrapped: wrapped}
}
