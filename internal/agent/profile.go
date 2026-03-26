package agent

// ResolvedProfile is the runtime profile resolved from a named profile.
// Separate from model.AgentSpec to decouple runtime defaults from workflow blueprints.
//
// Intentional asymmetry with AgentSpec:
//   - ResolvedProfile has ToolIDs/SkillIDs (runtime resolution needs explicit IDs)
//   - AgentSpec has no Tools field (tools are infrastructure, resolved by engine)
//   - AgentSpec is the workflow blueprint; ResolvedProfile is the runtime defaults
type ResolvedProfile struct {
	SystemPrompt  string   // empty = use engine DefaultSystemPrompt
	ToolIDs       []string // nil = use core tools; empty = no tools (explicit deny)
	SkillIDs      []string // nil/empty = no skills
	Model         string   // empty = use request model or engine default
	MaxIterations int      // 0 = use engine DefaultMaxIterations
	Temperature   *float64 // nil = use model default
	ThinkingLevel string   // empty = disabled
}
