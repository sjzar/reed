package tool

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// gitignoreMatcher provides lightweight root-level .gitignore matching.
// Known limitation: only reads the .gitignore at the search root, not nested ones.
type gitignoreMatcher struct {
	patterns []ignorePattern
}

type ignorePattern struct {
	pattern  string
	negate   bool
	dirOnly  bool
	anchored bool
}

// loadGitignore reads root/.gitignore and returns a matcher.
// Returns nil if the file doesn't exist or is unreadable.
func loadGitignore(root string) *gitignoreMatcher {
	f, err := os.Open(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []ignorePattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := ignorePattern{}
		if strings.HasPrefix(line, "!") {
			p.negate = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		// A pattern with "/" (other than trailing) is anchored to root
		if strings.Contains(line, "/") {
			p.anchored = true
		}
		p.pattern = line
		patterns = append(patterns, p)
	}
	if len(patterns) == 0 {
		return nil
	}
	return &gitignoreMatcher{patterns: patterns}
}

// Match returns true if relPath should be ignored.
// relPath must be relative to the search root. isDir indicates if the entry is a directory.
func (m *gitignoreMatcher) Match(relPath string, isDir bool) bool {
	if m == nil || len(m.patterns) == 0 {
		return false
	}
	// Normalize to forward slashes for matching
	relPath = filepath.ToSlash(relPath)
	ignored := false
	for _, p := range m.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if matchPattern(p.pattern, relPath, p.anchored) {
			ignored = !p.negate
		}
	}
	return ignored
}

// matchPattern checks if relPath matches the given pattern.
// Anchored patterns match from root; unanchored match any path segment.
func matchPattern(pattern, relPath string, anchored bool) bool {
	if anchored {
		ok, _ := doublestar.PathMatch(pattern, relPath)
		return ok
	}
	// Unanchored: try matching against full path and against basename
	ok, _ := doublestar.PathMatch(pattern, relPath)
	if ok {
		return true
	}
	// Also try matching against just the basename
	ok, _ = doublestar.PathMatch(pattern, filepath.Base(relPath))
	return ok
}
