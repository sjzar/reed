package bus

// LifecyclePayload carries engine lifecycle state changes.
type LifecyclePayload struct {
	Timestamp string `json:"ts"`
	ProcessID string `json:"processID"`
	RunID     string `json:"runID,omitempty"`
	StepRunID string `json:"stepRunID,omitempty"`
	JobID     string `json:"jobID,omitempty"`
	StepID    string `json:"stepID,omitempty"`
	Type      string `json:"type"`
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
}

// TextPayload carries streaming text output from a step.
type TextPayload struct {
	Delta string `json:"delta"`
}

// StatusPayload carries a status message from a step.
type StatusPayload struct {
	Message string `json:"message"`
}

// PromptPayload carries an interactive prompt from a step.
type PromptPayload struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

// AnswerPayload carries an answer to a prompt.
type AnswerPayload struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// SteerPayload carries a steering message to a step.
type SteerPayload struct {
	Message string `json:"message"`
}

// ParseLifecycle extracts a LifecyclePayload from a Message.
func ParseLifecycle(msg Message) (LifecyclePayload, bool) {
	p, ok := msg.Payload.(LifecyclePayload)
	return p, ok
}

// ParseText extracts a TextPayload from a Message.
func ParseText(msg Message) (TextPayload, bool) {
	p, ok := msg.Payload.(TextPayload)
	return p, ok
}

// ParseStatus extracts a StatusPayload from a Message.
func ParseStatus(msg Message) (StatusPayload, bool) {
	p, ok := msg.Payload.(StatusPayload)
	return p, ok
}

// ParsePrompt extracts a PromptPayload from a Message.
func ParsePrompt(msg Message) (PromptPayload, bool) {
	p, ok := msg.Payload.(PromptPayload)
	return p, ok
}

// ParseAnswer extracts an AnswerPayload from a Message.
func ParseAnswer(msg Message) (AnswerPayload, bool) {
	p, ok := msg.Payload.(AnswerPayload)
	return p, ok
}

// ParseSteer extracts a SteerPayload from a Message.
func ParseSteer(msg Message) (SteerPayload, bool) {
	p, ok := msg.Payload.(SteerPayload)
	return p, ok
}
