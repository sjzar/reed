package configpatch

import (
	"fmt"
	"maps"
)

// MergeRFC7386 returns a new top-level map with RFC 7386 merge-patch semantics.
// Nested maps are merged recursively; non-map values and arrays are replaced.
// Note: the result may share nested references with base and patch.
//
// Rules:
//   - Map: deep recursive merge (same key overrides, new key appends)
//   - Array: wholesale replacement
//   - Scalar: last wins
//   - Null: tombstone deletion
func MergeRFC7386(base, patch map[string]any) map[string]any {
	if base == nil {
		base = make(map[string]any)
	}
	result := make(map[string]any, len(base))
	maps.Copy(result, base)
	for k, pv := range patch {
		if pv == nil {
			// Null tombstone: delete key
			delete(result, k)
			continue
		}
		patchMap, patchIsMap := toMap(pv)
		baseVal, baseExists := result[k]
		if patchIsMap && baseExists {
			baseMap, baseIsMap := toMap(baseVal)
			if baseIsMap {
				// Both are maps: deep merge
				result[k] = MergeRFC7386(baseMap, patchMap)
				continue
			}
		}
		// Array, scalar, or type mismatch: direct replacement
		result[k] = pv
	}
	return result
}

// MergeAll applies a sequence of patches to a base map.
func MergeAll(base map[string]any, patches ...map[string]any) map[string]any {
	result := base
	for _, p := range patches {
		result = MergeRFC7386(result, p)
	}
	return result
}

func toMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		// YAML sometimes produces map[any]any
		result := make(map[string]any, len(m))
		for k, val := range m {
			result[fmt.Sprint(k)] = val
		}
		return result, true
	}
	return nil, false
}
