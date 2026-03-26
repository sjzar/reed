package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/session"
)

// defaultCompressPrompt is the system prompt used for context compaction.
const defaultCompressPrompt = `You are a conversation summarizer for an AI agent's working memory.

Summarize the following conversation into a concise summary. Output only the summary text.

## Retention Priority (highest to lowest)
1. Architectural decisions and design choices made during the conversation
2. Unfinished tasks, pending actions, and open questions
3. Key constraints, requirements, and acceptance criteria
4. Error paths attempted and their outcomes (what failed and why)
5. Tool output details (compress aggressively — keep conclusions, drop raw data)

## Identifier Preservation
Preserve ALL identifiers exactly as they appear — do not paraphrase, abbreviate, or round:
- File paths, URLs, and directory names
- UUIDs, commit hashes, PR/issue numbers
- Port numbers, IP addresses, hostnames
- Variable names, function names, package names
- Version numbers and error codes

## Format
- Use bullet points for distinct facts
- Group related items under short headers
- Omit conversational filler, acknowledgments, and restated instructions`

// IsContextExceeded checks if the error indicates a context length overflow.
func IsContextExceeded(err error) bool {
	var aiErr *model.AIError
	if errors.As(err, &aiErr) {
		return aiErr.Kind == model.ErrContextOverflow
	}
	return false
}

// ResponsesFunc is a function that sends a streaming LLM request.
type ResponsesFunc func(ctx context.Context, req *model.Request) (model.ResponseStream, error)

// llmCompressor implements session.LLMCompressor using a ResponsesFunc.
type llmCompressor struct {
	responses ResponsesFunc
	model     string
}

// NewCompressor creates an LLMCompressor that uses the given responses function for summarization.
func NewCompressor(responses ResponsesFunc, modelRef string) session.LLMCompressor {
	return &llmCompressor{responses: responses, model: modelRef}
}

// Compress generates a summary of the given messages using the LLM.
func (c *llmCompressor) Compress(ctx context.Context, messages []model.Message) (string, error) {
	// Build a conversation transcript for summarization
	var buf strings.Builder
	for _, msg := range messages {
		if msg.Role == model.RoleSystem {
			continue // skip system messages from transcript
		}
		fmt.Fprintf(&buf, "[%s]: %s\n", msg.Role, msg.ContentSummary())
	}

	stream, err := c.responses(ctx, &model.Request{
		Model: c.model,
		Messages: []model.Message{
			model.NewTextMessage(model.RoleSystem, defaultCompressPrompt),
			model.NewTextMessage(model.RoleUser, buf.String()),
		},
		MaxTokens: 2048,
	})
	if err != nil {
		return "", fmt.Errorf("compress llm call: %w", err)
	}
	resp, err := model.DrainStream(ctx, stream)
	if err != nil {
		return "", fmt.Errorf("compress drain stream: %w", err)
	}
	return resp.Content, nil
}
