package worker

import (
	"encoding/json"
	"strings"
)

// tryParseJSONOutput attempts to parse s as a JSON object or array.
// Returns the parsed value and true if successful. Scalar JSON values
// (strings, numbers, booleans, null) are intentionally not parsed.
func tryParseJSONOutput(s string) (any, bool) {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) == 0 {
		return nil, false
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return nil, false
	}
	var parsed any
	if json.Unmarshal([]byte(trimmed), &parsed) != nil {
		return nil, false
	}
	return parsed, true
}
