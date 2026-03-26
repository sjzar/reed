package mcp

import (
	"strings"
	"unicode"
)

// MatchKind indicates the confidence level of a fuzzy match.
type MatchKind string

const (
	MatchAuto    MatchKind = "auto"    // high confidence, auto-correct
	MatchSuggest MatchKind = "suggest" // lower confidence, suggest
)

// MatchResult holds the result of a fuzzy name match.
type MatchResult struct {
	Name string
	Kind MatchKind
}

// matchName tries to find the best match for input among candidates.
// Returns nil if no match is found.
// Strategy: exact → normalized → case-insensitive → Levenshtein.
func matchName(input string, candidates []string) *MatchResult {
	if len(candidates) == 0 {
		return nil
	}

	// 1. Exact match
	for _, c := range candidates {
		if input == c {
			return &MatchResult{Name: c, Kind: MatchAuto}
		}
	}

	// 2. Normalized match (strip non-alphanumeric, lowercase)
	normInput := normalizeIdentifier(input)
	for _, c := range candidates {
		if normalizeIdentifier(c) == normInput {
			return &MatchResult{Name: c, Kind: MatchAuto}
		}
	}

	// 3. Case-insensitive match
	lowerInput := strings.ToLower(input)
	for _, c := range candidates {
		if strings.ToLower(c) == lowerInput {
			return &MatchResult{Name: c, Kind: MatchAuto}
		}
	}

	// 4. Levenshtein distance with adaptive threshold
	bestDist := -1
	bestName := ""
	for _, c := range candidates {
		d := levenshtein(strings.ToLower(input), strings.ToLower(c))
		maxLen := len(input)
		if len(c) > maxLen {
			maxLen = len(c)
		}
		threshold := maxLen * 3 / 10 // floor(max_length * 0.3)
		if threshold < 2 {
			threshold = 2
		}
		if d <= threshold && (bestDist < 0 || d < bestDist) {
			bestDist = d
			bestName = c
		}
	}
	if bestName != "" {
		kind := MatchSuggest
		if bestDist <= 1 {
			kind = MatchAuto
		}
		return &MatchResult{Name: bestName, Kind: kind}
	}

	return nil
}

// normalizeIdentifier strips non-alphanumeric characters and lowercases.
func normalizeIdentifier(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	// Use single-row optimization
	prev := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr := make([]int, lb+1)
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min3(del, ins, sub)
		}
		prev = curr
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
