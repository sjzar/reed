package skill

import (
	"testing"
)

func TestComputeDigest(t *testing.T) {
	t.Run("deterministic output", func(t *testing.T) {
		files := []ResolvedFile{
			{Path: "a.txt", Content: []byte("hello")},
			{Path: "b.txt", Content: []byte("world")},
		}
		d1 := ComputeDigest(files)
		d2 := ComputeDigest(files)
		if d1 != d2 {
			t.Fatalf("expected same digest, got %q and %q", d1, d2)
		}
	})

	t.Run("order independent", func(t *testing.T) {
		filesAB := []ResolvedFile{
			{Path: "a.txt", Content: []byte("hello")},
			{Path: "b.txt", Content: []byte("world")},
		}
		filesBA := []ResolvedFile{
			{Path: "b.txt", Content: []byte("world")},
			{Path: "a.txt", Content: []byte("hello")},
		}
		dAB := ComputeDigest(filesAB)
		dBA := ComputeDigest(filesBA)
		if dAB != dBA {
			t.Fatalf("expected same digest regardless of order, got %q and %q", dAB, dBA)
		}
	})

	t.Run("different files produce different digest", func(t *testing.T) {
		files1 := []ResolvedFile{
			{Path: "a.txt", Content: []byte("hello")},
		}
		files2 := []ResolvedFile{
			{Path: "a.txt", Content: []byte("goodbye")},
		}
		d1 := ComputeDigest(files1)
		d2 := ComputeDigest(files2)
		if d1 == d2 {
			t.Fatal("expected different digests for different content")
		}
	})

	t.Run("different paths produce different digest", func(t *testing.T) {
		files1 := []ResolvedFile{
			{Path: "a.txt", Content: []byte("hello")},
		}
		files2 := []ResolvedFile{
			{Path: "b.txt", Content: []byte("hello")},
		}
		d1 := ComputeDigest(files1)
		d2 := ComputeDigest(files2)
		if d1 == d2 {
			t.Fatal("expected different digests for different paths")
		}
	})

	t.Run("empty files list", func(t *testing.T) {
		d := ComputeDigest(nil)
		if d == "" {
			t.Fatal("expected non-empty digest for empty file list")
		}
		// Should be the SHA-256 of empty input
		d2 := ComputeDigest([]ResolvedFile{})
		if d != d2 {
			t.Fatalf("nil and empty slice should produce same digest, got %q and %q", d, d2)
		}
	})
}

func TestDigestDirName(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		digest string
		want   string
	}{
		{
			name:   "normal case truncates to 12 chars",
			id:     "my-skill",
			digest: "abcdef123456789000",
			want:   "my-skill_abcdef123456",
		},
		{
			name:   "short digest kept as-is",
			id:     "sk",
			digest: "abc",
			want:   "sk_abc",
		},
		{
			name:   "exactly 12 chars",
			id:     "skill",
			digest: "abcdef123456",
			want:   "skill_abcdef123456",
		},
		{
			name:   "empty digest",
			id:     "skill",
			digest: "",
			want:   "skill_",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DigestDirName(tt.id, tt.digest)
			if got != tt.want {
				t.Errorf("DigestDirName(%q, %q) = %q, want %q", tt.id, tt.digest, got, tt.want)
			}
		})
	}
}
