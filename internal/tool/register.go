package tool

// BuiltinOption configures RegisterBuiltins.
type BuiltinOption func(*builtinConfig)

type builtinConfig struct {
	subAgentRunner SubAgentRunner
}

// WithSubAgentRunner sets the SubAgentRunner for the spawn_subagent tool.
// If not set, the subagent tool is still registered but returns an error on Execute.
func WithSubAgentRunner(r SubAgentRunner) BuiltinOption {
	return func(c *builtinConfig) { c.subAgentRunner = r }
}

// RegisterBuiltins registers all built-in tools into the given registry.
// All tools are registered unconditionally; access control is handled at runtime
// via permissions and tool list filtering, not at registration time.
func RegisterBuiltins(reg *Registry, opts ...BuiltinOption) error {
	var cfg builtinConfig
	for _, o := range opts {
		o(&cfg)
	}

	return reg.Register(
		NewReadTool(),
		NewWriteTool(),
		NewEditTool(),
		NewLSTool(),
		NewSearchTool(),
		NewBashTool(),
		NewSubAgentTool(cfg.subAgentRunner),
	)
}
