package ai

import (
	"fmt"
	"strings"

	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/model"
)

// candidate is a single model+provider pair in a failover queue.
type candidate struct {
	providerIdx int
	providerID  string
	modelID     string              // ForwardID — the actual ID sent to the API
	metadata    model.ModelMetadata // capabilities info
}

// matchModelFamily returns the handler type for a model ID based on prefix rules.
func matchModelFamily(modelID string) (base.HandlerType, bool) {
	for _, ht := range []base.HandlerType{
		base.HandlerTypeOpenAI,
		base.HandlerTypeAnthropic,
	} {
		if base.MatchesHandler(ht, modelID) {
			return ht, true
		}
	}
	return "", false
}

// resolve resolves a modelRef (possibly comma-separated) into an ordered
// failover queue. Empty modelRef falls back to the service default.
func (s *Service) resolve(modelRef string) ([]candidate, error) {
	if modelRef == "" {
		modelRef = s.defaultRef
	}
	if modelRef == "" {
		return nil, fmt.Errorf("ai: no model specified and no default configured")
	}

	var queue []candidate
	segments := strings.Split(modelRef, ",")
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		entries, err := s.resolveSegment(seg)
		if err != nil {
			return nil, err
		}
		queue = append(queue, entries...)
	}

	// If queue is empty and we haven't tried the default yet, try it
	if len(queue) == 0 && modelRef != s.defaultRef && s.defaultRef != "" {
		return s.resolve(s.defaultRef)
	}
	if len(queue) == 0 {
		return nil, fmt.Errorf("ai: no providers available for model %q", modelRef)
	}
	return queue, nil
}

// resolveSegment resolves a single segment (either "provider/model" or bare "model").
//
// Resolution levels:
//  1. provider/model — explicit provider reference
//  2. Bare model, explicit index lookup — model registered in modelIdx
//  3. Bare model, wildcard fallback with family disambiguation
func (s *Service) resolveSegment(seg string) ([]candidate, error) {
	provID, modelID := splitRef(seg)

	// Level 1: Direct provider/model lookup
	if provID != "" {
		idx, ok := s.providerBy[provID]
		if !ok {
			return nil, fmt.Errorf("ai: unknown provider %q", provID)
		}
		p := s.providers[idx]
		if _, ok := p.models[modelID]; ok {
			return []candidate{s.makeEntry(idx, modelID)}, nil
		}
		// Wildcard provider: passthrough escape hatch for provider/model syntax
		if p.wildcard {
			return []candidate{s.makeEntry(idx, modelID)}, nil
		}
		return nil, fmt.Errorf("ai: model %q not found in provider %q", modelID, provID)
	}

	// Level 2: Bare model → expand across all providers that serve it (config order)
	indices, ok := s.modelIdx[modelID]
	if ok && len(indices) > 0 {
		entries := make([]candidate, 0, len(indices))
		for _, idx := range indices {
			entries = append(entries, s.makeEntry(idx, modelID))
		}
		return entries, nil
	}

	// Level 3: Wildcard fallback with family disambiguation
	var wildcards []int
	for idx, p := range s.providers {
		if p.wildcard {
			wildcards = append(wildcards, idx)
		}
	}

	switch len(wildcards) {
	case 0:
		return nil, fmt.Errorf("ai: unknown model %q", modelID)
	case 1:
		// Single wildcard provider — passthrough any model
		return []candidate{s.makeEntry(wildcards[0], modelID)}, nil
	default:
		// Multiple wildcard providers — disambiguate by model family
		family, matched := matchModelFamily(modelID)
		if !matched {
			return nil, fmt.Errorf("ai: ambiguous model %q — multiple providers available; use provider/model syntax (e.g. openai/%s)", modelID, modelID)
		}
		// Find the unique wildcard provider matching this family
		var match []int
		for _, idx := range wildcards {
			if s.providers[idx].handlerType == family {
				match = append(match, idx)
			}
		}
		if len(match) == 1 {
			return []candidate{s.makeEntry(match[0], modelID)}, nil
		}
		if len(match) == 0 {
			return nil, fmt.Errorf("ai: model %q matches family %q but no wildcard provider of that type is available", modelID, family)
		}
		return nil, fmt.Errorf("ai: ambiguous model %q — multiple providers of type %q; use provider/model syntax", modelID, family)
	}
}

// makeEntry builds a candidate for the given provider index and model ID.
func (s *Service) makeEntry(provIdx int, modelID string) candidate {
	p := s.providers[provIdx]
	entry := candidate{
		providerIdx: provIdx,
		providerID:  p.id,
		modelID:     modelID,
	}
	if me, ok := p.models[modelID]; ok {
		entry.modelID = me.forwardID
		entry.metadata = me.metadata
	}
	// Merge built-in capabilities as fallback for nil/zero-valued fields.
	if spec, ok := base.LookupModel(entry.modelID); ok {
		fallback := spec.ToMetadata()
		if entry.metadata.ContextWindow == 0 {
			entry.metadata.ContextWindow = fallback.ContextWindow
		}
		if entry.metadata.MaxTokens == 0 {
			entry.metadata.MaxTokens = fallback.MaxTokens
		}
		if entry.metadata.Thinking == nil {
			entry.metadata.Thinking = fallback.Thinking
		}
		if entry.metadata.Vision == nil {
			entry.metadata.Vision = fallback.Vision
		}
		if entry.metadata.Streaming == nil {
			entry.metadata.Streaming = fallback.Streaming
		}
	}
	return entry
}
