---
title: Worker Overview
summary: Worker interface, Factory, Router dispatch, registered worker types, and the Build dependency chain.
read_when: Adding a new worker type, understanding step execution routing, or debugging worker construction.
---

## Worker Interface

Defined in `internal/engine/interfaces.go`:

```go
type Worker interface {
    Execute(ctx context.Context, payload StepPayload) StepRunResult
}
```

Workers are blackbox executors. They receive a fully-rendered `StepPayload` (all expressions evaluated by the engine owner loop) and return a `StepRunResult`. Workers have zero knowledge of DAG, upstream/downstream, or workflow context.

## StepPayload

```go
type StepPayload struct {
    StepRunID  string
    JobID      string
    StepID     string
    Uses       string            // worker type selector
    With       map[string]any    // step-specific parameters
    Env        map[string]string // inherited + overlaid env vars
    WorkDir    string
    Shell      string            // optional shell override
    Background bool
    Timeout    int               // seconds; 0 = use default

    ToolAccess model.ToolAccessMode // "workdir" | "full", from workflow.ToolAccess

    // Expression-evaluated agent spec overrides (set by dispatch for agent steps)
    RenderedAgentModel        string
    RenderedAgentSystemPrompt string

    // Event bus for publishing step output. nil = discard events.
    Bus *bus.Bus
}
```

## StepRunResult

```go
type StepRunResult struct {
    StepRunID    string
    JobID        string
    StepID       string
    Status       model.StepStatus   // StepSucceeded | StepFailed
    Outputs      map[string]any
    ErrorCode    string
    ErrorMessage string
}
```

## Router

`Router` (`internal/worker/router.go`) implements `engine.Worker` by dispatching based on `payload.Uses`.

Default registrations in `NewRouter()`:
- `"shell"` -> `ShellWorker`
- `"bash"` -> `ShellWorker` (same instance)
- `"run"` -> `ShellWorker` (same instance)
- `"http"` -> `HTTPWorker`

`Register(uses, worker)` adds custom workers. Unknown `Uses` values return `StepFailed` with `"unknown uses"` error.

## Factory

`Factory` (`internal/worker/factory.go`) holds global resources and creates Router instances. `NewFactory(wf, agentRunner, coreToolIDs, skillSvc)`.

`Factory.NewRouter()` creates a `Router` (via `NewRouter()` for defaults) then registers:
- `"agent"` -> `AgentWorker` (constructed with workflow, agent.Runner, coreToolIDs, skillSvc)

## Build Function

`Build(ctx, BuildConfig) (*BuildResult, error)` (`internal/worker/build.go`) constructs the full dependency chain.

### BuildConfig

```go
type BuildConfig struct {
    Workflow    *model.Workflow
    WorkDir     string
    Models      conf.ModelsConfig
    RouteStore  session.RouteStore
    SessionDir  string
    SkillDir    string
    SkillModDir string
    MemoryDir   string
    Media       *media.LocalService // nil = no media support
}
```

### Construction Sequence

1. **MCP pool**: `mcp.NewPool()` with transport factory. Loads and inits workflow `mcp_servers` asynchronously with a cancellable context. If any later step fails, `cancelInit()` + `pool.StopAll()` are deferred for cleanup.
2. **AI service**: `ai.New(cfg.Models)`.
3. **Session service**: `session.New(sessionDir, routeStore, nil, nil, session.WithInbox(sessionDir))`.
4. **Tool registry + MCP tools**: `tool.NewRegistry()`, wraps MCP pool tools via `tool.WrapMCPTools()`, registers each with `tool.GroupMCP`.
5. **Tool service**: `tool.NewService(reg, tool.WithSession(sessionSvc))`.
6. **Skill service**: `skill.New(skillDir, skillModDir)`, scans installed skills from WorkDir, loads workflow-declared skills (resolved relative to `filepath.Dir(cfg.Workflow.Source)`, not `cfg.WorkDir`), validates all skill refs are resolvable via `workflow.ValidateSkillRefsResolvable()`.
7. **Memory**: `ai.NewMemoryExtractor(aiService.Responses, defaultModel)` + `memory.NewFileProvider(memoryDir, extractor)`.
8. **Agent runner**: Optionally wraps AI service with media resolver middleware (`middleware.NewMedia`) if `cfg.Media != nil`. Creates `agent.New(agentAI, sessionSvc, agent.WrapToolService(toolSvc), skillSvc, memProvider, ...opts)` with optional `agent.WithMedia()`.
9. **SubAgent runner + builtins**: `agent.NewSubAgentRunner(agentRunner, ...)`, then `tool.RegisterBuiltins(reg, tool.WithSubAgentRunner(subRunner))`.
10. **Factory + Router**: `NewFactory(wf, agentRunner, reg.CoreToolIDs(), skillSvc).NewRouter()`.

### BuildResult

```go
type BuildResult struct {
    Router *Router   // final Worker implementation
    Pool   *mcp.Pool // MCP pool; caller must call Pool.StopAll() on shutdown
}
```

`BuildResult.Close()` calls `Pool.StopAll()`. Safe to call on nil receiver.

### Error Handling

- Nil workflow -> error immediately.
- Invalid skill references -> error after scan/load.
- Deferred cleanup: if any construction step fails, the init context is cancelled and the MCP pool is stopped before returning the error.
