---
title: Model Types Reference
summary: Complete type reference for internal/model — workflow, runtime, AI/LLM, event, API, DB types
read_when: looking up type definitions, understanding data structures, working with any model type
---

# Model Types Reference

Package: `internal/model` (leaf dependency -- never imports other internal packages)

## Workflow Types (workflow.go)

### Workflow

Top-level workflow definition. Key fields: `App`, `Name`, `Version`, `Description`, `Source` (programmatic, not YAML), `On` (OnSpec), `ToolAccess` ("workdir"/"full"), `RunJobs`, `Inputs` (map[string]InputSpec), `Outputs` (map[string]string), `Env`, `Agents` (map[string]AgentSpec), `Skills` (map[string]SkillSpec), `MCPServers` (map[string]MCPServerSpec), `Jobs` (map[string]Job), `Metadata` (map[string]any).

### OnSpec

Trigger configuration. Fields: `CLI` (*CLITrigger), `Service` (*ServiceTrigger), `Schedule` ([]ScheduleRule). CLI and Service are mutually exclusive. CLI and Schedule are mutually exclusive.

### CLITrigger

Fields: `Commands` (map[string]CLICommand).

### CLICommand

Fields: `Description`, `RunJobs`, `Inputs` (map[string]InputSpec), `Outputs` (map[string]string).

### ServiceTrigger

Fields: `Port` (int), `HTTP` ([]HTTPRoute), `MCP` ([]MCPTool).

### HTTPRoute

Fields: `Path`, `Method`, `Async` (bool), `RunJobs`, `Inputs`, `Outputs`, `Concurrency` (*ConcurrencySpec).

### ConcurrencySpec

Fields: `Group` (partition key, supports expressions), `Behavior` ("queue"/"skip"/"replace-pending"/"cancel-in-progress"/"steer"). Default behavior: "queue".

### MCPTool

Fields: `Name`, `Description`, `RunJobs`, `Inputs`, `Outputs`.

### ScheduleRule

Fields: `Cron` (string), `RunJobs` ([]string).

### InputSpec

Fields: `Type`, `Required` (bool), `Default` (any), `Description`.

### AgentSpec

Fields: `Model`, `SystemPrompt`, `Skills` ([]string), `MaxIterations`, `Temperature` (*float64), `ThinkingLevel`.

### SkillSpec

Fields: `Uses` (path), `Resources` ([]SkillResourceSpec). Mutually exclusive.

### SkillResourceSpec

Fields: `Path`, `Content`.

### MCPServerSpec

Fields: `Transport` ("stdio"/"sse"/"streamable-http"), `Command`, `Args`, `URL`, `Env`, `Header`.

### Job

Fields: `ID` (set from map key), `Needs` ([]string), `Steps` ([]Step), `Outputs` (map[string]string).

### Step

Fields: `ID`, `Uses`, `Run` (DSL sugar), `With` (map[string]any), `If`, `Timeout` (seconds), `WorkDir`, `Shell`, `Env`, `Background` (bool).

## Runtime Types (process.go)

### ProcessMode

Enum: `cli`, `service`, `schedule`.

### ProcessStatus

Enum: `STARTING`, `RUNNING`, `STOPPED`, `FAILED`.

### Process

Fields: `ID`, `PID`, `Mode` (ProcessMode), `Status` (ProcessStatus), `CreatedAt`, `UpdatedAt`.

### RunStatus

Enum: `CREATED`, `STARTING`, `RUNNING`, `STOPPING`, `SUCCEEDED`, `FAILED`, `CANCELED`.

### Run

Fields: `ID`, `ProcessID`, `Workflow` (*Workflow), `WorkflowSource`, `Status` (RunStatus), `CreatedAt`, `StartedAt`, `FinishedAt` (*time.Time). All runs are memory-only.

### StepStatus

Enum: `PENDING`, `RUNNING`, `SUCCEEDED`, `FAILED`, `CANCELED`, `SKIPPED`.

### StepRun

Fields: `ID`, `RunID`, `JobID`, `StepID`, `Background` (bool), `Status` (StepStatus), `Outputs` (map[string]any), `ErrorCode`, `ErrorMessage`, `StartedAt`, `FinishedAt`.

## Message Types (message.go)

### Role

Enum: `system`, `user`, `assistant`, `tool`.

### Content

Fields: `Type` ("text"/"thinking"/"image"/"document"), `Text`, `Signature`, `MediaURI`, `MIMEType`, `Filename`.

### Message

Fields: `Role`, `Content` ([]Content), `ToolCalls` ([]ToolCall), `ToolCallID`, `ToolName`, `IsError`, `Usage` (*Usage). Methods: `Clone()`, `TextContent()`, `ThinkingContent()`, `ContentSummary()`, `HasImageContent()`, `HasMediaContent()`, `ImageContentCount()`, `MediaContentCount()`.

### ToolCall

Fields: `ID`, `Name`, `Arguments` (map[string]any), `RawJSON`.

### Usage

Fields: `Input`, `Output`, `CacheRead`, `CacheWrite`, `Total`.

## LLM Types (llm.go)

### Thinking

Fields: `Content` (string), `Signature`.

### ToolDef

Fields: `Name`, `Description`, `InputSchema` (map[string]any -- JSON Schema), `Summary`.

### Request

Fields: `Target`, `Model`, `AgentID`, `Messages`, `Tools`, `MaxTokens`, `Temperature`, `TopP`, `Stop`, `EstimatedTokens`, `Schema`, `Thinking`, `TimeoutMs`, `ExtraParams`.

### Response

Fields: `Content` (string), `Thinking` (*Thinking), `ToolCalls`, `StopReason` (StopReason), `Usage`.

### StopReason

Enum: `end_turn`, `tool_use`, `max_tokens`.

### ResponseStream

Interface: `Next(ctx) (StreamEvent, error)`, `Close()`, `Response()`.

### StreamEvent

Fields: `Type` (StreamEventType), `Delta`, `ToolCall`. Types: `text_delta`, `toolcall_delta`, `thinking_delta`, `done`.

### ToolResult

Fields: `Content` ([]Content), `IsError`. Methods: `ContentSize()`, `Truncate(maxBytes)`.

### ModelMetadata

Fields: `Name`, `Thinking` (*bool), `Vision` (*bool), `Streaming` (*bool), `ContextWindow`, `MaxTokens`.

Helper functions: `DrainStream(ctx, stream)`, `BoolPtr(b)`, `BoolVal(b)`.

## LLM Error Types (llm_error.go)

### ErrorKind

Enum: `ErrOther`, `ErrRateLimit`, `ErrAuth`, `ErrContextOverflow`, `ErrServerError`, `ErrNetwork`.

### AIError

Fields: `Kind` (ErrorKind), `StatusCode`, `Message`, `Retryable`, `RetryAfter`, `Err`.

Helper: `ClassifyError(err, statusCode, body)`.

## Event Types (event.go)

### EventType

Enum: `llm_start`, `llm_end`, `text_chunk`, `tool_start`, `tool_log`, `tool_end`, `tool_dispatch`, `tool_complete`, `tool_cancel`, `status`, `subagent_spawn`, `subagent_complete`, `error`.

### Event

Fields: `Type` (EventType), `Timestamp`, `Data` (any).

### Event Data Types

| Type | Fields |
|---|---|
| `LLMStartData` | Iteration, Model, Messages, Tools |
| `LLMEndData` | StopReason, ToolCalls, Usage |
| `ToolStartData` | ToolCallID, ToolName, Arguments |
| `ToolEndData` | ToolCallID, ToolName, Result, IsError, Duration |
| `ToolDispatchData` | ToolCallID, ToolName, JobID |
| `ToolCompleteData` | JobID, ToolCallID, ToolName, IsError, Duration |
| `ToolCancelData` | JobID, ToolName |
| `SubAgentSpawnData` | ID, AgentID, Prompt |
| `SubAgentCompleteData` | ID, Output, Error |
| `TextChunkData` | Delta |
| `StatusData` | Status, Message |
| `ErrorData` | Error, Code, Details |

## Agent Types (agent.go)

### AgentRunRequest

Session routing: `Namespace`, `AgentID`, `SessionKey`, `SessionID`. Template: `Profile`, `Tools`, `Skills`. Model params: `Model`, `MaxTokens`, `Temperature`, `TopP`, `ThinkingLevel`, `ExtraParams`, `Schema`. Input: `SystemPrompt`, `Prompt`, `Media`. Loop: `MaxIterations`, `TimeoutMs`, `WaitForAsyncTasks`. Internal (not serialized): `Bus`, `StepRunID`, `RunRoot`, `Env`, `ToolAccessProfile`, `Cwd`, `PromptMode`.

### AgentStopReason

Enum: `complete`, `max_iterations`, `max_tokens`, `canceled`, `loop_detected`, `async_pending`.

### AgentRunResponse

Fields: `Output`, `Messages`, `TotalUsage`, `Iterations`, `StopReason`.

## Session Types (session.go)

### SessionEntry

Unified JSONL persistence format. Fields: `Type` ("message"/"compaction"/"custom"), `ID`, `ParentID`, `Timestamp` (unix millis), `Meta`. Message fields: `Message`, `StopReason`, `IsError`. Custom fields: `CustomType`, `Data`. Compaction fields: `Cursor`, `Summary`, `TokensBefore`.

Constructors: `NewMessageSessionEntry(msg, parentID)`, `NewCompactionSessionEntry(cursor, summary, tokensBefore)`, `NewCustomSessionEntry(customType, data)`.

## Run Request Types (run_request.go)

### TriggerType

Enum: `cli`, `http`, `mcp`, `schedule`.

### TriggerParams

Fields: `TriggerType`, `TriggerMeta`, `RunJobs`, `Inputs`, `InputValues`, `Outputs`, `Env`.

### RunRequest

Fields: `Workflow`, `WorkflowSource`, `TriggerType`, `TriggerMeta`, `Inputs`, `Outputs`, `Env`, `Secrets`, `WorkDir`, `Timeout`.

## API View Models (api_view.go)

### PingResponse

Fields: `ProcessID`, `PID`, `Mode`, `Now`. JSON: lowerCamelCase.

### StatusView

Fields: `ProcessID`, `PID`, `Mode`, `Status`, `Uptime`, `CreatedAt`, `Listeners` ([]ListenerView), `ActiveRuns` ([]ActiveRunView).

### ListenerView

Fields: `Protocol`, `Address`, `RouteCount`.

### ActiveRunView

Fields: `RunID`, `WorkflowSource`, `Status`, `CreatedAt`, `StartedAt`, `FinishedAt`, `Jobs` (map[string]APIJobView).

### APIJobView

Fields: `JobID`, `Status`, `Outputs`, `Steps` (map[string]APIStepView).

### APIStepView

Fields: `StepID`, `StepRunID`, `Status`, `IsBackground`, `Outputs`, `ErrorCode`, `ErrorMessage`.

## DB Row Models (db_row.go)

### ProcessRow

Fields: `ID`, `PID`, `Mode`, `Status`, `WorkflowSource`, `CreatedAt`, `UpdatedAt`, `MetadataJSON`.

### SessionRouteRow

Fields: `Namespace`, `AgentID`, `SessionKey`, `CurrentSessionID`, `UpdatedAt`.

### MediaEntry

Fields: `ID`, `MIMEType`, `Size`, `StoragePath`, `ExpiresAt`, `CreatedAt`.
