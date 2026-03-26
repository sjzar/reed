// Package token provides LLM token estimation utilities.
package token

import (
	"math"
)

const (
	// Base weights (based on ~4 chars per token standard).
	// Latin: 1 char ≈ 0.25 tokens
	// CJK: 1 char ≈ 1.2 tokens (with safety buffer)
	// Special: 1 char ≈ 2.0 tokens (emoji / rare characters)
	weightLatin   = 0.25
	weightCJK     = 1.2
	weightSpecial = 2.0
)

// Estimate returns an approximate token count for the given string.
// It uses character-class weights (Latin, CJK, special) with a 2% variance buffer.
func Estimate(s string) int {
	if s == "" {
		return 0
	}

	var total float64
	for _, r := range s {
		switch {
		// 1. ASCII range (English, half-width punctuation)
		case r <= 127:
			total += weightLatin

		// 2. CJK character sets and full-width punctuation
		case (r >= 0x2E80 && r <= 0x9FFF) ||
			(r >= 0xF900 && r <= 0xFAFF) ||
			(r >= 0xFF00 && r <= 0xFFEF) ||
			(r >= 0x3000 && r <= 0x303F):
			total += weightCJK

		// 3. Supplementary planes (emoji, special symbols)
		case r > 0xFFFF:
			total += weightSpecial

		// 4. Other (extended Latin, etc.)
		default:
			total += 0.5
		}
	}

	// Round up and add 2% system-level variance buffer
	return int(math.Ceil(total * 1.02))
}
