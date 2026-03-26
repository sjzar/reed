package openai_responses

import "github.com/sjzar/reed/internal/ai/base"

func init() {
	base.RegisterHandler(base.HandlerTypeOpenAIResponses, New)
}
