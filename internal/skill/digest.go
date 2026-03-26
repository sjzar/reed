package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// ComputeDigest computes a SHA-256 hex digest over all resolved files.
// Files are sorted by path for deterministic output.
func ComputeDigest(files []ResolvedFile) string {
	sorted := make([]ResolvedFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f.Path))
		h.Write([]byte{0})
		h.Write(f.Content)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// DigestDirName returns a directory name combining skill ID and digest prefix.
func DigestDirName(id, digest string) string {
	if len(digest) > 12 {
		digest = digest[:12]
	}
	return id + "_" + digest
}
