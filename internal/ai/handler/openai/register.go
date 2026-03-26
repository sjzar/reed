package openai

import "github.com/sjzar/reed/internal/ai/base"

func init() {
	base.RegisterHandler(base.HandlerTypeOpenAI, New)
}
