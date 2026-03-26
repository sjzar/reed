package anthropic

import "github.com/sjzar/reed/internal/ai/base"

func init() {
	base.RegisterHandler(base.HandlerTypeAnthropic, New)
}
