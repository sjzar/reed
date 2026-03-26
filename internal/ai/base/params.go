package base

// ExtraParams key constants for provider-specific parameters.
const (
	ExtraKeyServiceTier  = "service_tier"  // OpenAI: "auto"/"default"
	ExtraKeyCacheControl = "cache_control" // Anthropic: cache control settings
)

// ExtractString returns a string value from ExtraParams.
func ExtractString(params map[string]any, key string) (string, bool) {
	if params == nil {
		return "", false
	}
	v, ok := params[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// ExtractBool returns a bool value from ExtraParams.
func ExtractBool(params map[string]any, key string) (bool, bool) {
	if params == nil {
		return false, false
	}
	v, ok := params[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

// ExtractInt returns an int value from ExtraParams (handles float64 from JSON).
func ExtractInt(params map[string]any, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// ExtractFloat returns a float64 value from ExtraParams.
func ExtractFloat(params map[string]any, key string) (float64, bool) {
	if params == nil {
		return 0, false
	}
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

// ToStringSlice extracts a []string from a value that may be []any or []string.
func ToStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []string:
		return val
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
