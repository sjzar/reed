package ai

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/model"
)

// keyEnvByType maps handler type → env var name for API key auto-injection.
var keyEnvByType = map[base.HandlerType]string{
	base.HandlerTypeOpenAI:    "OPENAI_API_KEY",
	base.HandlerTypeAnthropic: "ANTHROPIC_API_KEY",
}

// officialHosts lists hosts where auto key injection is safe.
var officialHosts = map[base.HandlerType][]string{
	base.HandlerTypeOpenAI:    {"api.openai.com"},
	base.HandlerTypeAnthropic: {"api.anthropic.com"},
}

// injectKey auto-fills pc.Key from the environment variable when the key is
// empty and the provider points to an official endpoint (or has no BaseURL).
func injectKey(pc *conf.ProviderConfig) {
	if pc.Key != "" {
		return
	}
	envVar, ok := keyEnvByType[base.HandlerType(pc.Type)]
	if !ok {
		return
	}
	key := os.Getenv(envVar)
	if key == "" {
		return
	}
	// No BaseURL → official endpoint assumed → inject.
	if pc.BaseURL == "" {
		pc.Key = key
		return
	}
	// BaseURL set → inject only if hostname matches an official host.
	parsed, err := url.Parse(pc.BaseURL)
	if err != nil {
		return
	}
	host := parsed.Hostname()
	for _, h := range officialHosts[base.HandlerType(pc.Type)] {
		if strings.EqualFold(host, h) {
			pc.Key = key
			return
		}
	}
}

// providerEntry is an internal record for a registered provider.
type providerEntry struct {
	id          string
	handlerType base.HandlerType // for model family routing
	handler     base.RoundTripper
	models      map[string]modelEntry // model ID → entry
	wildcard    bool                  // true for env-fallback providers — accepts any model ID
}

// modelEntry is an internal record for a model within a provider.
type modelEntry struct {
	forwardID string
	metadata  model.ModelMetadata
}

// Service is the AI service that provides failover-aware LLM dispatch.
type Service struct {
	providers  []providerEntry
	providerBy map[string]int   // provider ID → index in providers
	modelIdx   map[string][]int // model ID → provider indices (config order)
	defaultRef string
	penalty    *penaltyBox
}

// New creates a Service from the given ModelsConfig.
// Handlers are discovered from the global registry (populated by init() in handler packages).
// Optional extras can be provided for testing or custom handlers.
func New(cfg conf.ModelsConfig, extras ...map[base.HandlerType]base.HandlerFactory) (*Service, error) {
	handlers := base.RegisteredHandlers()
	for _, extra := range extras {
		for k, v := range extra {
			handlers[k] = v
		}
	}

	s := &Service{
		providerBy: make(map[string]int),
		modelIdx:   make(map[string][]int),
		defaultRef: cfg.Default,
		penalty:    newPenaltyBox(),
	}

	providers := make([]conf.ProviderConfig, len(cfg.Providers))
	copy(providers, cfg.Providers)
	if len(providers) == 0 {
		providers = envFallbackProviders()
	}

	for i := range providers {
		pc := &providers[i]
		if pc.Disabled {
			continue
		}
		// Auto-inject API key from env for explicit providers with empty key.
		injectKey(pc)

		factory, ok := handlers[base.HandlerType(pc.Type)]
		if !ok {
			return nil, fmt.Errorf("ai: unknown handler type %q for provider %q", pc.Type, pc.ID)
		}

		h, err := factory(*pc)
		if err != nil {
			return nil, fmt.Errorf("ai: create handler for provider %q: %w", pc.ID, err)
		}

		idx := len(s.providers)
		entry := providerEntry{
			id:          pc.ID,
			handlerType: base.HandlerType(pc.Type),
			handler:     h,
			models:      make(map[string]modelEntry),
			wildcard:    pc.EnvFallback,
		}
		for _, mc := range pc.Models {
			fwd := mc.ForwardID
			if fwd == "" {
				fwd = mc.ID
			}
			entry.models[mc.ID] = modelEntry{
				forwardID: fwd,
				metadata: model.ModelMetadata{
					Name:          mc.Name,
					Thinking:      mc.Thinking,
					Vision:        mc.Vision,
					Streaming:     mc.Streaming,
					ContextWindow: mc.ContextWindow,
					MaxTokens:     mc.MaxTokens,
				},
			}
			s.modelIdx[mc.ID] = append(s.modelIdx[mc.ID], idx)
		}
		s.providerBy[pc.ID] = idx
		s.providers = append(s.providers, entry)
	}

	return s, nil
}

// Responses is the sole entry point for LLM requests.
// It resolves the model from req.Model (or defaultRef), builds a failover queue,
// and dispatches the streaming request.
func (s *Service) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	queue, err := s.resolve(req.Model)
	if err != nil {
		return nil, err
	}
	ft := &failoverTripper{service: s, queue: queue, agentID: req.AgentID}
	return ft.dispatch(ctx, req)
}

// Chat is a convenience method that calls Responses and drains the stream.
func (s *Service) Chat(ctx context.Context, req *model.Request) (*model.Response, error) {
	stream, err := s.Responses(ctx, req)
	if err != nil {
		return nil, err
	}
	return model.DrainStream(ctx, stream)
}

// ModelMetadataFor returns the metadata of the primary model for the given ref.
func (s *Service) ModelMetadataFor(modelRef string) model.ModelMetadata {
	queue, err := s.resolve(modelRef)
	if err != nil || len(queue) == 0 {
		return model.ModelMetadata{}
	}
	return queue[0].metadata
}

// failoverAction determines what to do after an error.
type failoverAction int

const (
	actionFailover failoverAction = iota
	actionHardStop
)

// classifyForFailover maps an error to a failover action and penalty TTL.
func classifyForFailover(err error) (failoverAction, time.Duration) {
	var aiErr *model.AIError
	if !errors.As(err, &aiErr) {
		// Unknown error — conservative failover
		return actionFailover, shortPenaltyTTL
	}

	switch aiErr.Kind {
	case model.ErrContextOverflow:
		return actionHardStop, 0
	case model.ErrRateLimit:
		return actionFailover, shortPenaltyTTL
	case model.ErrServerError:
		return actionFailover, shortPenaltyTTL
	case model.ErrNetwork:
		return actionFailover, shortPenaltyTTL
	case model.ErrAuth:
		return actionFailover, longPenaltyTTL
	case model.ErrOther:
		// Check if it's a 400 — hard stop
		if aiErr.StatusCode == 400 {
			return actionHardStop, 0
		}
		// Non-400 unknown errors — conservative failover
		return actionFailover, shortPenaltyTTL
	default:
		return actionFailover, shortPenaltyTTL
	}
}

// cloneRequest creates a shallow copy of the request with a different model ID.
func cloneRequest(req *model.Request, modelID string) *model.Request {
	clone := *req
	clone.Model = modelID
	return &clone
}

// splitRef splits "provider/model" into (provider, model).
// If no slash, returns ("", ref).
func splitRef(ref string) (provider, modelName string) {
	if before, after, ok := strings.Cut(ref, "/"); ok {
		return before, after
	}
	return "", ref
}

// envFallbackProviders auto-registers providers from environment variables.
// Each provider includes well-known models for that provider type.
func envFallbackProviders() []conf.ProviderConfig {
	var providers []conf.ProviderConfig
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		providers = append(providers, conf.ProviderConfig{
			ID:          "openai",
			Type:        string(base.HandlerTypeOpenAI),
			Key:         key,
			Models:      envFallbackModels("openai"),
			EnvFallback: true,
		})
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		providers = append(providers, conf.ProviderConfig{
			ID:          "anthropic",
			Type:        string(base.HandlerTypeAnthropic),
			Key:         key,
			Models:      envFallbackModels("anthropic"),
			EnvFallback: true,
		})
	}
	return providers
}

// providerHandlerType maps env-fallback provider IDs to their handler types.
var providerHandlerType = map[string]base.HandlerType{
	"openai":    base.HandlerTypeOpenAI,
	"anthropic": base.HandlerTypeAnthropic,
}

// envFallbackModels returns well-known model configs for a provider.
func envFallbackModels(provider string) []conf.ModelConfig {
	ht, ok := providerHandlerType[provider]
	if !ok {
		return nil
	}
	var models []conf.ModelConfig
	for _, id := range base.KnownModelIDs() {
		if base.MatchesHandler(ht, id) {
			models = append(models, conf.ModelConfig{ID: id})
		}
	}
	return models
}
