package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"github.com/sjzar/reed/pkg/jsonutil"
)

// canonicalJSON produces a deterministic JSON string for comparison.
func canonicalJSON(v map[string]any) string {
	return jsonutil.DeterministicMarshal(v)
}

// hashPrefix returns the first n hex chars of the SHA-256 hash of s.
func hashPrefix(s string, n int) string {
	return jsonutil.HashPrefix(s, n)
}

// repetitionDetector detects when the agent is stuck in a loop.
type repetitionDetector struct {
	window int
	recent []string
}

func newRepetitionDetector(window int) *repetitionDetector {
	return &repetitionDetector{window: window}
}

// recordRound records a round of tool calls, generating a round hash.
func (d *repetitionDetector) recordRound(calls []roundCall) {
	sort.Slice(calls, func(i, j int) bool {
		return calls[i].Name < calls[j].Name
	})
	h := sha256.New()
	for _, c := range calls {
		h.Write([]byte(c.Name))
		h.Write([]byte(c.Input))
		h.Write([]byte(c.Output))
	}
	digest := hex.EncodeToString(h.Sum(nil))[:32]
	d.recent = append(d.recent, digest)
	if len(d.recent) > d.window {
		d.recent = d.recent[len(d.recent)-d.window:]
	}
}

// roundCall records a single tool call within a round.
type roundCall struct {
	Name   string
	Input  string
	Output string
}

// detected returns true if the last window rounds have identical hashes.
func (d *repetitionDetector) detected() bool {
	if len(d.recent) < d.window {
		return false
	}
	first := d.recent[0]
	for _, r := range d.recent[1:] {
		if r != first {
			return false
		}
	}
	return true
}
