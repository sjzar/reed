package reed

import (
	"context"
	"fmt"

	"github.com/sjzar/reed/internal/agent"
	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/session"
	"github.com/sjzar/reed/internal/skill"
)

// BuildAgentConfig is the input for building a standalone agent (no workflow).
type BuildAgentConfig struct {
	WorkDir     string
	Models      conf.ModelsConfig
	RouteStore  session.RouteStore
	SessionDir  string
	HomeDir     string // ~/.reed/ — used for skill manifests
	SkillModDir string
	MemoryDir   string
}

// BuildAgentResult contains the constructed agent runner and related resources.
type BuildAgentResult struct {
	Runner      *agent.Runner
	Skills      *skill.Service   // skill service for CLI skill resolution
	CoreToolIDs []string         // core tool IDs for default tool selection
	SessionSvc  *session.Service // session service; caller must Close() to release JSONL writers
}

// Close cleans up resources held by BuildAgentResult.
func (r *BuildAgentResult) Close() {
	if r == nil {
		return
	}
	if r.SessionSvc != nil {
		_ = r.SessionSvc.Close()
	}
}

// BuildAgent constructs an agent engine without a workflow.
// It mirrors the dependency chain in Build() but omits MCP, workflow skills,
// factory, and router.
func BuildAgent(ctx context.Context, cfg BuildAgentConfig) (*BuildAgentResult, error) {
	aiService, sessionSvc, err := newAIServices(cfg.Models, cfg.SessionDir, cfg.RouteStore)
	if err != nil {
		return nil, err
	}

	// Tool registry (no MCP tools)
	reg, toolSvc, err := newTooling(sessionSvc, nil)
	if err != nil {
		return nil, err
	}

	// Skill service: scan installed only (no workflow skills)
	skillSvc := skill.New(cfg.HomeDir, cfg.SkillModDir)
	if err := skillSvc.ScanInstalled(ctx, cfg.WorkDir); err != nil {
		return nil, fmt.Errorf("scan installed skills: %w", err)
	}

	agentRunner, err := newAgent(ctx, agentEngineDeps{
		aiService:  aiService,
		sessionSvc: sessionSvc,
		toolSvc:    toolSvc,
		reg:        reg,
		skillSvc:   skillSvc,
		models:     cfg.Models,
		memoryDir:  cfg.MemoryDir,
	})
	if err != nil {
		return nil, err
	}

	return &BuildAgentResult{
		Runner:      agentRunner,
		Skills:      skillSvc,
		CoreToolIDs: reg.CoreToolIDs(),
		SessionSvc:  sessionSvc,
	}, nil
}
