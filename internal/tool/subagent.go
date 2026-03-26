package tool

import (
	"context"

	"github.com/sjzar/reed/internal/model"
)

// SubAgentRequest carries the minimal input for spawning a subagent.
type SubAgentRequest struct {
	AgentID string
	Prompt  string
	Env     map[string]string
	RunRoot string
	Depth   int // current nesting depth (0 = top-level agent)
}

// SubAgentRunner executes a subagent synchronously and returns the structured response.
type SubAgentRunner interface {
	RunSubAgent(ctx context.Context, req SubAgentRequest) (*model.AgentRunResponse, error)
}
