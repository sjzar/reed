// Package configpatch provides generic patch operations on map[string]any
// configuration documents.
//
// Two complementary strategies are offered:
//
//   - RFC 7386 merge-patch ([MergeRFC7386], [MergeAll]): deep recursive map
//     merge with null-tombstone deletion and wholesale array replacement.
//   - Dot-path assignment ([ApplySet], [ApplySetString]): imperative key=value
//     overrides with type inference, bracket array indexing, and auto-vivification.
//
// # Mutation Semantics
//
// [MergeRFC7386] returns a new top-level map. Nested maps are merged
// recursively; the result may share nested references with base and patch.
// If full isolation is required, deep-copy the inputs before merging.
//
// [ApplySet] and [ApplySetString] modify the provided map in place.
// The returned map is the same reference that was passed in (or a freshly
// allocated one when nil was provided). On error, the map may be partially
// mutated — callers that need atomicity should operate on a copy.
package configpatch
