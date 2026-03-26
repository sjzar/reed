package agent

import "github.com/sjzar/reed/internal/model"

// runState holds the mutable runtime state for an agent loop iteration.
type runState struct {
	messages         []model.Message
	totalUsage       model.Usage
	iteration        int
	repDetector      *repetitionDetector
	lastToolBoundary int // index in messages where current iteration's tool results start
	lastCompactIter  int // iteration at which compaction last ran (-1 = never)
}

// newRunState creates a runState with a default repetition detector (window=3).
func newRunState() *runState {
	return &runState{
		repDetector:     newRepetitionDetector(3),
		lastCompactIter: -1,
	}
}

// appendMessages appends messages to the state.
func (s *runState) appendMessages(msgs ...model.Message) {
	s.messages = append(s.messages, msgs...)
}

// addUsage accumulates token usage.
func (s *runState) addUsage(u model.Usage) {
	s.totalUsage = addUsage(s.totalUsage, u)
}

// addUsage sums two Usage values.
func addUsage(a, b model.Usage) model.Usage {
	return model.Usage{
		Input:      a.Input + b.Input,
		Output:     a.Output + b.Output,
		CacheRead:  a.CacheRead + b.CacheRead,
		CacheWrite: a.CacheWrite + b.CacheWrite,
		Total:      a.Total + b.Total,
	}
}

// buildResponse constructs the final AgentRunResponse from current state.
func (s *runState) buildResponse(reason model.AgentStopReason) *model.AgentRunResponse {
	return buildRunResponse(s.messages, s.totalUsage, s.iteration, reason)
}
