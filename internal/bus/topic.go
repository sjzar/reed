package bus

import "fmt"

// Well-known topic constants.
const (
	TopicLifecycle = "engine.lifecycle"
)

// StepOutputTopic returns the output topic for a step run: "step.<stepRunID>.output".
func StepOutputTopic(stepRunID string) string {
	return fmt.Sprintf("step.%s.output", stepRunID)
}

// StepInputTopic returns the input topic for a step run: "step.<stepRunID>.input".
func StepInputTopic(stepRunID string) string {
	return fmt.Sprintf("step.%s.input", stepRunID)
}
