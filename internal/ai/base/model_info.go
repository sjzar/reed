package base

import (
	"strings"

	"github.com/sjzar/reed/internal/model"
)

// KnownModelSpec describes static capabilities of a known model.
// Used as fallback when conf.ModelConfig doesn't set a field.
type KnownModelSpec struct {
	Thinking      bool
	Vision        bool
	Streaming     bool
	ContextWindow int
	MaxTokens     int
}

// ToMetadata converts static spec to runtime ModelMetadata.
// Bool capabilities map to *bool pointers; zero-valued ints are left as zero.
func (s KnownModelSpec) ToMetadata() model.ModelMetadata {
	return model.ModelMetadata{
		Thinking:      model.BoolPtr(s.Thinking),
		Vision:        model.BoolPtr(s.Vision),
		Streaming:     model.BoolPtr(s.Streaming),
		ContextWindow: s.ContextWindow,
		MaxTokens:     s.MaxTokens,
	}
}

// LookupModel returns known capabilities for a model ID.
// Uses exact match first, then longest prefix match.
func LookupModel(modelID string) (KnownModelSpec, bool) {
	if caps, ok := knownModels[modelID]; ok {
		return caps, true
	}
	var best string
	for k := range knownModels {
		if strings.HasPrefix(modelID, k) && len(k) > len(best) {
			best = k
		}
	}
	if best != "" {
		return knownModels[best], true
	}
	return KnownModelSpec{}, false
}

// KnownModelIDs returns all known model IDs.
func KnownModelIDs() []string {
	ids := make([]string, 0, len(knownModels))
	for k := range knownModels {
		ids = append(ids, k)
	}
	return ids
}
