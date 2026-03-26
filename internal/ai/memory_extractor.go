package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/sjzar/reed/internal/memory"
	"github.com/sjzar/reed/internal/model"
)

// memoryExtractor implements memory.LLMExtractor using a ResponsesFunc.
type memoryExtractor struct {
	responses ResponsesFunc
	modelRef  string
}

// NewMemoryExtractor creates a memory.LLMExtractor backed by the given LLM.
func NewMemoryExtractor(responses ResponsesFunc, modelRef string) memory.LLMExtractor {
	return &memoryExtractor{responses: responses, modelRef: modelRef}
}

// ExtractFacts extracts memorable facts from a conversation.
func (e *memoryExtractor) ExtractFacts(ctx context.Context, messages []model.Message, existingMemory string) (string, error) {
	var buf strings.Builder
	if existingMemory != "" {
		fmt.Fprintf(&buf, "## Existing memory\n%s\n\n", existingMemory)
	} else {
		buf.WriteString("## Existing memory\n(empty)\n\n")
	}

	buf.WriteString("## Conversation\n")
	for _, msg := range messages {
		if msg.Role == model.RoleSystem {
			continue
		}
		fmt.Fprintf(&buf, "[%s]: %s\n", msg.Role, msg.TextContent())
	}

	stream, err := e.responses(ctx, &model.Request{
		Model: e.modelRef,
		Messages: []model.Message{
			model.NewTextMessage(model.RoleSystem, memory.ExtractionSystemPrompt),
			model.NewTextMessage(model.RoleUser, buf.String()),
		},
		MaxTokens: 1024,
	})
	if err != nil {
		return "", fmt.Errorf("memory extraction llm call: %w", err)
	}
	resp, err := model.DrainStream(ctx, stream)
	if err != nil {
		return "", fmt.Errorf("memory extraction drain stream: %w", err)
	}
	return strings.TrimSpace(resp.Content), nil
}
