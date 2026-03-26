package base

import (
	"strings"
	"sync"

	"github.com/sjzar/reed/internal/conf"
)

// HandlerType identifies the wire protocol a provider uses.
type HandlerType string

const (
	HandlerTypeOpenAI          HandlerType = "openai-completions"
	HandlerTypeOpenAIResponses HandlerType = "openai-responses"
	HandlerTypeAnthropic       HandlerType = "anthropic-messages"
)

// HandlerFactory creates a RoundTripper for a given provider configuration.
type HandlerFactory func(cfg conf.ProviderConfig) (RoundTripper, error)

var (
	registryMu sync.RWMutex
	registry   = make(map[HandlerType]HandlerFactory)
)

// RegisterHandler registers a handler factory for the given type.
// Typically called from init() in handler packages.
func RegisterHandler(handlerType HandlerType, factory HandlerFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[handlerType] = factory
}

// RegisteredHandlers returns a snapshot of all registered handler factories.
func RegisteredHandlers() map[HandlerType]HandlerFactory {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make(map[HandlerType]HandlerFactory, len(registry))
	for k, v := range registry {
		out[k] = v
	}
	return out
}

// handlerModelPrefixes maps handler types to known model ID prefixes.
var handlerModelPrefixes = map[HandlerType][]string{
	HandlerTypeOpenAI:          {"gpt-", "o1", "o3", "o4"},
	HandlerTypeOpenAIResponses: {"gpt-", "o1", "o3", "o4"},
	HandlerTypeAnthropic:       {"claude-"},
}

// ModelPrefixes returns the known model ID prefixes for a handler type.
func ModelPrefixes(ht HandlerType) []string {
	return handlerModelPrefixes[ht]
}

// MatchesHandler reports whether modelID belongs to the given handler type by prefix.
func MatchesHandler(ht HandlerType, modelID string) bool {
	for _, prefix := range handlerModelPrefixes[ht] {
		if strings.HasPrefix(modelID, prefix) {
			return true
		}
	}
	return false
}
