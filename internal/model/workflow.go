package model

// Workflow represents a parsed and validated workflow definition (the "blueprint").
type Workflow struct {
	App         string `yaml:"app"`
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description"`
	Source      string `yaml:"-"` // set programmatically, not from YAML

	On         OnSpec                   `yaml:"on"`
	ToolAccess string                   `yaml:"tool_access"` // "workdir" (default) | "full"
	RunJobs    []string                 `yaml:"run_jobs"`    // default DAG entrypoint filter
	Inputs     map[string]InputSpec     `yaml:"inputs"`
	Outputs    map[string]string        `yaml:"outputs"`
	Env        map[string]string        `yaml:"env"`
	Agents     map[string]AgentSpec     `yaml:"agents"`
	Skills     map[string]SkillSpec     `yaml:"skills"`
	MCPServers map[string]MCPServerSpec `yaml:"mcp_servers"`
	Jobs       map[string]Job           `yaml:"jobs"`
	Metadata   map[string]any           `yaml:"metadata"`
}

// OnSpec defines the trigger mode for a workflow.
// CLI and Service are mutually exclusive. CLI and Schedule are mutually exclusive.
// Service and Schedule can coexist.
type OnSpec struct {
	CLI      *CLITrigger     `yaml:"cli"`
	Service  *ServiceTrigger `yaml:"service"`
	Schedule []ScheduleRule  `yaml:"schedule"`
}

// CLITrigger defines ephemeral batch mode execution with optional subcommands.
type CLITrigger struct {
	Commands map[string]CLICommand `yaml:"commands"`
}

// CLICommand defines a named subcommand within a CLI trigger.
type CLICommand struct {
	Description string               `yaml:"description"`
	RunJobs     []string             `yaml:"run_jobs"`
	Inputs      map[string]InputSpec `yaml:"inputs"`
	Outputs     map[string]string    `yaml:"outputs"`
}

// ServiceTrigger defines daemon mode execution.
type ServiceTrigger struct {
	Port int         `yaml:"port"`
	HTTP []HTTPRoute `yaml:"http"`
	MCP  []MCPTool   `yaml:"mcp"`
}

// HTTPRoute defines an HTTP trigger within a service workflow.
type HTTPRoute struct {
	Path        string               `yaml:"path"`
	Method      string               `yaml:"method"`
	Async       bool                 `yaml:"async,omitempty"`
	RunJobs     []string             `yaml:"run_jobs"`
	Inputs      map[string]InputSpec `yaml:"inputs"`
	Outputs     map[string]string    `yaml:"outputs"`
	Concurrency *ConcurrencySpec     `yaml:"concurrency,omitempty"`
}

const (
	ConcurrencyBehaviorQueue          = "queue"
	ConcurrencyBehaviorSkip           = "skip"
	ConcurrencyBehaviorReplacePending = "replace-pending"
	ConcurrencyBehaviorCancelProgress = "cancel-in-progress"
	ConcurrencyBehaviorSteer          = "steer"
)

// ConcurrencySpec defines how concurrent runs of the same route should be handled.
type ConcurrencySpec struct {
	// Group is the concurrency partition key, for example "${{ inputs.session_id }}".
	Group string `yaml:"group"`

	// Behavior defines how a new request is handled when the same group is busy.
	// Implemented:
	// - "queue": strict FIFO queueing (default)
	// - "skip": reject the new request
	// - "replace-pending": keep the running request and retain only the newest pending request
	// Reserved for future implementation:
	// - "cancel-in-progress"
	// - "steer"
	Behavior string `yaml:"behavior"`
}

// EffectiveBehavior returns the runtime behavior, defaulting to queue.
func (s *ConcurrencySpec) EffectiveBehavior() string {
	if s == nil || s.Behavior == "" {
		return ConcurrencyBehaviorQueue
	}
	return s.Behavior
}

// MCPTool defines an MCP tool trigger within a service workflow.
type MCPTool struct {
	Name        string               `yaml:"name"`
	Description string               `yaml:"description"`
	RunJobs     []string             `yaml:"run_jobs"`
	Inputs      map[string]InputSpec `yaml:"inputs"`
	Outputs     map[string]string    `yaml:"outputs"`
}

// ScheduleRule defines a cron-based trigger.
type ScheduleRule struct {
	Cron    string   `yaml:"cron"`
	RunJobs []string `yaml:"run_jobs"`
}

// InputSpec defines a workflow input parameter.
type InputSpec struct {
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required"`
	Default     any    `yaml:"default"`
	Description string `yaml:"description"`
}

// AgentSpec defines an agent role (model + system prompt + skill refs + control params).
type AgentSpec struct {
	Model         string   `yaml:"model"`
	SystemPrompt  string   `yaml:"system_prompt"`
	Skills        []string `yaml:"skills"`
	MaxIterations int      `yaml:"max_iterations,omitempty"`
	Temperature   *float64 `yaml:"temperature,omitempty"`
	ThinkingLevel string   `yaml:"thinking_level,omitempty"`
}

// SkillSpec defines a skill reference in a workflow.
type SkillSpec struct {
	Uses      string              `yaml:"uses"`
	Resources []SkillResourceSpec `yaml:"resources"`
}

// SkillResourceSpec defines an additional resource file for a skill.
type SkillResourceSpec struct {
	Path    string `yaml:"path"`
	Content string `yaml:"content"`
}

// MCPServerSpec defines an MCP server connection configuration.
type MCPServerSpec struct {
	Transport string            `yaml:"transport"`
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	URL       string            `yaml:"url"`
	Env       map[string]string `yaml:"env"`
	Header    map[string]string `yaml:"header"`
}

// Job represents a unit of work within a workflow DAG.
type Job struct {
	ID      string            `yaml:"-"` // set from map key in postProcess
	Needs   []string          `yaml:"needs"`
	Steps   []Step            `yaml:"steps"`
	Outputs map[string]string `yaml:"outputs"`
}

// Step represents a single executable action within a job.
type Step struct {
	ID         string            `yaml:"id"`
	Uses       string            `yaml:"uses"`
	Run        string            `yaml:"run"` // DSL sugar; postProcess promotes to With["run"]
	With       map[string]any    `yaml:"with"`
	If         string            `yaml:"if"`
	Timeout    int               `yaml:"timeout"` // seconds; 0 = use default
	WorkDir    string            `yaml:"workdir"`
	Shell      string            `yaml:"shell" json:"shell,omitempty"`
	Env        map[string]string `yaml:"env"`
	Background bool              `yaml:"background"`
}
