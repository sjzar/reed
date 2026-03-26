package media

import "sort"

// CollectURIsFromMap extracts media:// URIs from a string-keyed map.
// It handles string values, []any, and []string slices at the top level only;
// nested maps or deeper structures are not traversed. This matches the flat
// structure of workflow step "with" parameters.
// Keys are iterated in sorted order for deterministic output ordering.
func CollectURIsFromMap(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var uris []string
	for _, k := range keys {
		v := m[k]
		switch val := v.(type) {
		case string:
			if IsMediaURI(val) {
				uris = append(uris, val)
			}
		case []any:
			for _, item := range val {
				if s, ok := item.(string); ok && IsMediaURI(s) {
					uris = append(uris, s)
				}
			}
		case []string:
			for _, s := range val {
				if IsMediaURI(s) {
					uris = append(uris, s)
				}
			}
		}
	}
	return uris
}
