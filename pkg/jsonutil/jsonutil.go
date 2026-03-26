// Package jsonutil provides JSON utilities for deterministic marshalling,
// hashing, and deep-cloning of JSON-compatible types.
package jsonutil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// DeterministicMarshal produces a deterministic JSON string from a map.
// Uses standard json.Marshal which sorts map keys alphabetically.
// Returns "{}" for nil maps or marshal errors.
func DeterministicMarshal(v map[string]any) string {
	if v == nil {
		return "{}"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// HashPrefix returns the first n hex chars of the SHA-256 hash of s.
func HashPrefix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	h := sha256.Sum256([]byte(s))
	hexStr := hex.EncodeToString(h[:])
	if n > len(hexStr) {
		n = len(hexStr)
	}
	return hexStr[:n]
}

// DeepClone returns a deep copy of v for JSON-compatible types.
// Supported composite types: map[string]any, []any, []string.
// These are recursively cloned, so nested combinations (e.g. map containing
// []any containing map) are fully copied. Scalar types (string, float64,
// bool, nil, json.Number) are immutable and returned as-is.
// Other composite types (e.g. a bare []map[string]any) pass through
// uncloned — callers must ensure the top-level value is one of the
// supported shapes.
func DeepClone(v any) any {
	switch val := v.(type) {
	case map[string]any:
		cp := make(map[string]any, len(val))
		for k, v2 := range val {
			cp[k] = DeepClone(v2)
		}
		return cp
	case []any:
		cp := make([]any, len(val))
		for i, v2 := range val {
			cp[i] = DeepClone(v2)
		}
		return cp
	case []string:
		cp := make([]string, len(val))
		copy(cp, val)
		return cp
	default:
		return v
	}
}
