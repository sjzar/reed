package configpatch

import (
	"fmt"
	"strconv"
	"strings"
)

// maxNestedLevel is the maximum nesting depth for dot-separated paths.
const maxNestedLevel = 30

// maxArrayIndex is the maximum allowed array index to prevent excessive allocation.
const maxArrayIndex = 65536

// tombstone is a sentinel value used to signal key deletion (RFC 7386 null semantics).
// It is unexported and never leaks into the final map.
type tombstone struct{}

// ApplySet modifies the provided map in place, applying dot-path key=value
// assignments with type inference. On error the map may be partially mutated.
//
// Each element of sets is a comma-separated list of key=value pairs.
// Keys use dot-separated paths with optional bracket array indexing.
// Type inference: true/false -> bool, null/~ -> tombstone delete, int -> int,
// float -> float64, else string.
func ApplySet(raw map[string]any, sets []string) (map[string]any, error) {
	if raw == nil {
		raw = make(map[string]any)
	}
	for _, s := range sets {
		if err := parseAndApply(raw, s, false); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// ApplySetString modifies the provided map in place, applying dot-path
// key=value assignments with all values forced to string (no type inference).
// On error the map may be partially mutated.
func ApplySetString(raw map[string]any, sets []string) (map[string]any, error) {
	if raw == nil {
		raw = make(map[string]any)
	}
	for _, s := range sets {
		if err := parseAndApply(raw, s, true); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// parseAndApply parses a comma-separated set expression and applies each pair to raw.
func parseAndApply(raw map[string]any, expr string, forceString bool) error {
	pairs := splitPairs(expr)
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return fmt.Errorf("configpatch: %q: missing '='", pair)
		}
		k = strings.TrimSpace(k)
		if k == "" {
			return fmt.Errorf("configpatch: %q: empty key", pair)
		}

		var val any
		if arr, ok := parseArrayLiteral(v, forceString); ok {
			val = arr
		} else {
			val = parseValue(v, forceString)
		}
		if err := setPath(raw, k, val, 0); err != nil {
			return fmt.Errorf("configpatch: %s: %w", k, err)
		}
	}
	return nil
}

// splitPairs splits a set expression on unescaped commas.
// Backslash-escaped commas (\,) are preserved as literal commas.
// Commas inside {...} array literals are not treated as separators.
func splitPairs(s string) []string {
	var pairs []string
	var buf strings.Builder
	escaped := false
	braceDepth := 0
	for _, r := range s {
		if escaped {
			if r == ',' {
				buf.WriteRune(',')
			} else {
				buf.WriteRune('\\')
				buf.WriteRune(r)
			}
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '{' {
			braceDepth++
			buf.WriteRune(r)
			continue
		}
		if r == '}' && braceDepth > 0 {
			braceDepth--
			buf.WriteRune(r)
			continue
		}
		if r == ',' && braceDepth == 0 {
			pairs = append(pairs, buf.String())
			buf.Reset()
			continue
		}
		buf.WriteRune(r)
	}
	if escaped {
		buf.WriteRune('\\')
	}
	if buf.Len() > 0 {
		pairs = append(pairs, buf.String())
	}
	return pairs
}

// parseValue converts a raw string value to a typed Go value.
// In forceString mode, the value is returned as-is.
// Otherwise: true/false -> bool, null/~ -> tombstone, integers -> int,
// floats -> float64, else string.
func parseValue(v string, forceString bool) any {
	if forceString {
		return v
	}
	if v == "true" {
		return true
	}
	if v == "false" {
		return false
	}
	if v == "null" || v == "~" {
		return tombstone{}
	}
	// Preserve leading zeros as strings (e.g., "00009" stays string, but "0" is int)
	if len(v) > 1 && v[0] == '0' && v[1] != '.' {
		return v
	}
	if i, err := strconv.ParseInt(v, 10, 64); err == nil {
		return int(i)
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	return v
}

// parseArrayLiteral checks if v starts with '{' and ends with '}',
// and if so parses it as a comma-separated array literal.
func parseArrayLiteral(v string, forceString bool) ([]any, bool) {
	if !strings.HasPrefix(v, "{") || !strings.HasSuffix(v, "}") {
		return nil, false
	}
	inner := v[1 : len(v)-1]
	if inner == "" {
		return []any{}, true
	}
	parts := splitPairs(inner)
	result := make([]any, len(parts))
	for i, p := range parts {
		result[i] = parseValue(p, forceString)
	}
	return result, true
}

// setPath sets a value at a dot-separated path with optional bracket indexing.
// Auto-vivifies intermediate maps and auto-expands arrays.
// A tombstone value triggers key deletion.
func setPath(data map[string]any, path string, val any, depth int) error {
	if depth > maxNestedLevel {
		return fmt.Errorf("nesting depth exceeds maximum of %d", maxNestedLevel)
	}

	// Parse the first segment: everything before the first unbracketed '.' or '['
	seg, rest, kind := nextSegment(path)
	if seg == "" && kind != segBracket {
		return fmt.Errorf("empty key segment in path %q", path)
	}

	switch kind {
	case segEnd:
		// Leaf: assign value
		if _, ok := val.(tombstone); ok {
			delete(data, seg)
			return nil
		}
		data[seg] = val
		return nil

	case segDot:
		// Intermediate map
		child := vivifyMap(data, seg)
		if child == nil {
			return fmt.Errorf("path %q: %q is not a map", path, seg)
		}
		return setPath(child, rest, val, depth+1)

	case segBracket:
		// Array index: seg[idx]...
		idxStr, afterBracket := parseBracketIndex(rest)
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			return fmt.Errorf("path %q: invalid array index %q", path, idxStr)
		}
		if idx < 0 {
			return fmt.Errorf("path %q: negative index %d not allowed", path, idx)
		}
		if idx > maxArrayIndex {
			return fmt.Errorf("path %q: index %d exceeds maximum of %d", path, idx, maxArrayIndex)
		}

		// Get or create the array
		arr := vivifyArray(data, seg)
		if arr == nil {
			return fmt.Errorf("path %q: %q is not an array", path, seg)
		}

		// Auto-expand
		if idx >= len(arr) {
			expanded := make([]any, idx+1)
			copy(expanded, arr)
			arr = expanded
		}

		if afterBracket == "" {
			// Leaf: assign to array element
			if _, ok := val.(tombstone); ok {
				arr[idx] = nil
			} else {
				arr[idx] = val
			}
			data[seg] = arr
			return nil
		}

		// Continue into nested structure
		if afterBracket[0] == '.' {
			afterBracket = afterBracket[1:]
			child, ok := arr[idx].(map[string]any)
			if !ok {
				child = make(map[string]any)
			}
			if err := setPath(child, afterBracket, val, depth+1); err != nil {
				return err
			}
			arr[idx] = child
			data[seg] = arr
			return nil
		}
		if afterBracket[0] == '[' {
			// Nested array index: arr[idx] is itself an array
			innerKey := "__inner__"
			innerArr := []any{}
			if existing, ok := arr[idx].([]any); ok {
				innerArr = existing
			}
			tmpMap := map[string]any{innerKey: innerArr}
			if err := setPath(tmpMap, innerKey+afterBracket, val, depth+1); err != nil {
				return err
			}
			arr[idx] = tmpMap[innerKey]
			data[seg] = arr
			return nil
		}
		return fmt.Errorf("path %q: unexpected character after ']'", path)
	}
	return nil
}

type segKind int

const (
	segEnd     segKind = iota // no more segments
	segDot                    // followed by '.'
	segBracket                // followed by '['
)

// nextSegment splits path into (segment, rest, kind).
// "a.b.c" -> ("a", "b.c", segDot)
// "a[0].b" -> ("a", "0].b", segBracket)
// "a" -> ("a", "", segEnd)
func nextSegment(path string) (string, string, segKind) {
	for i, r := range path {
		if r == '.' {
			return path[:i], path[i+1:], segDot
		}
		if r == '[' {
			return path[:i], path[i+1:], segBracket
		}
	}
	return path, "", segEnd
}

// parseBracketIndex extracts the index from "N]..." returning (N, rest-after-]).
func parseBracketIndex(s string) (string, string) {
	before, after, found := strings.Cut(s, "]")
	if !found {
		return s, ""
	}
	return before, after
}

// vivifyMap ensures data[key] is a map[string]any, creating it if absent.
// Returns nil if the existing value is not a map.
func vivifyMap(data map[string]any, key string) map[string]any {
	v, exists := data[key]
	if !exists {
		m := make(map[string]any)
		data[key] = m
		return m
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

// vivifyArray ensures data[key] is a []any, creating it if absent.
// Returns nil if the existing value is not an array.
func vivifyArray(data map[string]any, key string) []any {
	v, exists := data[key]
	if !exists {
		arr := []any{}
		data[key] = arr
		return arr
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	return arr
}
