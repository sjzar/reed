package reed

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"

	mcppkg "github.com/sjzar/reed/internal/mcp"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/pkg/cron"

	reedhttp "github.com/sjzar/reed/internal/http"
)

// startIPC creates the status provider, starts the UDS listener, and adds the IPC listener.
func (m *Manager) startIPC(processID string) error {
	if !m.ipcReady {
		return nil
	}
	if m.http == nil {
		m.http = reedhttp.New(reedhttp.Config{Debug: m.cfg.Debug})
	}
	provider := newStatusProvider(m.engine)
	m.http.SetStatusProvider(provider)
	if m.media != nil {
		m.http.SetMediaService(m.media)
	}
	if err := m.http.StartIPC(m.cfg.SockPath(processID)); err != nil {
		return err
	}
	m.engine.AddListener(model.ListenerView{
		Protocol: "ipc",
		Address:  m.cfg.SockPath(processID),
	})
	return nil
}

// setupHTTPTriggers reads m.wf and m.resolver to register HTTP/MCP trigger routes.
func (m *Manager) setupHTTPTriggers() {
	if m.wf == nil || m.wf.On.Service == nil || m.resolver == nil {
		return
	}
	m.SetupHTTPTriggers(m.Resolver(), m.wf, m.wfSource)
}

// setupScheduleTriggers reads m.wf and m.resolver to register schedule triggers.
func (m *Manager) setupScheduleTriggers() error {
	if m.wf == nil || m.resolver == nil || len(m.wf.On.Schedule) == 0 {
		return nil
	}
	return m.SetupScheduleTriggers(m.Resolver(), m.wf, m.wfSource)
}

// SetupHTTPTriggers registers HTTP and MCP trigger routes on the HTTP service.
func (m *Manager) SetupHTTPTriggers(resolver RunResolver, wf *model.Workflow, wfSource string) {
	if wf.On.Service == nil {
		return
	}

	dispatcher := reedhttp.NewHTTPTriggerDispatcher(resolver, m.engine, wf, wfSource, m.workDir, m.media)
	routes := wf.On.Service.HTTP
	if len(routes) == 0 && len(wf.On.Service.MCP) == 0 {
		routes = []model.HTTPRoute{{Path: "/", Method: "POST"}}
	}
	m.http.RegisterWorkflowRoutes(routes, dispatcher)

	if len(wf.On.Service.MCP) > 0 {
		handler := mcppkg.NewTriggerHandler(resolver, m.engine, wf, wfSource, m.workDir)
		mcpServer := mcppkg.NewMCPServer(wf.On.Service.MCP, handler.HandleToolCall)
		m.http.RegisterMCPHandler(mcppkg.NewStreamableHandler(mcpServer))
		m.engine.AddListener(model.ListenerView{
			Protocol:   "mcp",
			Address:    fmt.Sprintf("0.0.0.0:%d/mcp", wf.On.Service.Port),
			RouteCount: len(wf.On.Service.MCP),
		})
	}
}

// SetupScheduleTriggers creates the cron scheduler and registers rules.
func (m *Manager) SetupScheduleTriggers(resolver RunResolver, wf *model.Workflow, wfSource string) error {
	if len(wf.On.Schedule) == 0 {
		return nil
	}

	sched := cron.NewScheduler()
	for _, rule := range wf.On.Schedule {
		if err := sched.AddRule(rule.Cron, func() {
			params := model.TriggerParams{
				TriggerType: model.TriggerSchedule,
				TriggerMeta: map[string]any{"cron": rule.Cron},
			}
			if len(rule.RunJobs) > 0 {
				params.RunJobs = rule.RunJobs
			}
			req, err := resolver.ResolveRunRequest(context.Background(), wf, wfSource, params)
			if err != nil {
				log.Error().Err(err).Str("cron", rule.Cron).Msg("schedule trigger failed to resolve run request")
				return
			}
			req.WorkDir = m.workDir
			if _, err := m.engine.Submit(req); err != nil {
				log.Error().Err(err).Str("cron", rule.Cron).Msg("schedule trigger failed to submit run")
			}
		}); err != nil {
			return fmt.Errorf("add schedule rule %q: %w", rule.Cron, err)
		}
	}
	m.scheduler = sched
	return nil
}
