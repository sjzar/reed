// Package security provides centralized security primitives:
// path access control (Guard) and secret management (SecretStore).
package security

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AccessProfile controls the file-system boundary for tool execution.
type AccessProfile string

const (
	// ProfileWorkdir restricts tools to granted directory trees.
	ProfileWorkdir AccessProfile = "workdir"
	// ProfileFull allows tools to access any path on the host.
	ProfileFull AccessProfile = "full"
)

// Permission describes the type of access to a path.
type Permission int

const (
	// Read allows reading files and listing directories.
	Read Permission = 1 << iota
	// Write allows creating, modifying, and deleting files.
	Write
)

// Grant records a single path permission with an audit reason.
type Grant struct {
	Root       string     // canonicalized absolute path
	Permission Permission // Read, Write, or Read|Write
	Reason     string     // audit trail (e.g. "skill:kb-reader")
}

// Checker is the read-only interface exposed to tools via context.
// Tools cannot escalate privileges through this interface.
type Checker interface {
	CheckRead(resolved string) error
	CheckWrite(resolved string) error
}

// Forkable extends Checker with the ability to create child guards.
// Only engine/orchestration code should use this — tools only see Checker.
type Forkable interface {
	Checker
	Fork() *Guard
}

// Guard is a stateful security policy created per agent run.
// Only engine/orchestration code should hold *Guard; tools receive
// the read-only Checker interface via context.
type Guard struct {
	mu      sync.RWMutex
	profile AccessProfile
	grants  []Grant
}

// New creates a Guard with the given profile. If profile is workdir and cwd
// is non-empty, cwd is granted read+write access as the base working directory.
func New(profile AccessProfile, cwd string) *Guard {
	g := &Guard{profile: profile}
	if profile == ProfileWorkdir && cwd != "" {
		// cwd should already be canonicalized by the caller (newRuntimeContext),
		// but canonicalize defensively.
		if root, err := Canonicalize(cwd); err == nil {
			g.grants = []Grant{{Root: root, Permission: Read | Write, Reason: "cwd"}}
		}
	}
	return g
}

// GrantRead grants read access to root for the given reason.
// The root is canonicalized internally to prevent path traversal.
func (g *Guard) GrantRead(root, reason string) error {
	return g.grant(root, Read, reason)
}

// GrantWrite grants write access to root for the given reason.
// The root is canonicalized internally to prevent path traversal.
func (g *Guard) GrantWrite(root, reason string) error {
	return g.grant(root, Write, reason)
}

func (g *Guard) grant(root string, perm Permission, reason string) error {
	if root == "" {
		return fmt.Errorf("security: empty root")
	}
	canonical, err := Canonicalize(root)
	if err != nil {
		return fmt.Errorf("security: canonicalize %q: %w", root, err)
	}
	g.mu.Lock()
	g.grants = append(g.grants, Grant{Root: canonical, Permission: perm, Reason: reason})
	g.mu.Unlock()
	return nil
}

// CheckRead checks whether the resolved path is readable.
func (g *Guard) CheckRead(resolved string) error {
	return g.check(resolved, Read)
}

// CheckWrite checks whether the resolved path is writable.
func (g *Guard) CheckWrite(resolved string) error {
	return g.check(resolved, Write)
}

func (g *Guard) check(resolved string, perm Permission) error {
	if g.profile == ProfileFull {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, grant := range g.grants {
		if grant.Permission&perm == perm && isContainedIn(resolved, grant.Root) {
			return nil
		}
	}
	return fmt.Errorf("access denied: %s is outside allowed roots", resolved)
}

// Fork creates a child Guard that inherits all current grants.
// New grants on the child do not affect the parent (deep copy).
func (g *Guard) Fork() *Guard {
	g.mu.RLock()
	defer g.mu.RUnlock()
	newGrants := make([]Grant, len(g.grants))
	copy(newGrants, g.grants)
	return &Guard{
		profile: g.profile,
		grants:  newGrants,
	}
}

// Grants returns a snapshot of all current grants (for audit/debugging).
func (g *Guard) Grants() []Grant {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Grant, len(g.grants))
	copy(out, g.grants)
	return out
}

// --- context storage ---

// contextKey is an unexported type to prevent other packages from
// forging or overriding the context value.
type contextKey struct{}

var checkerKey = contextKey{}

// WithChecker stores a Checker in the context.
func WithChecker(ctx context.Context, c Checker) context.Context {
	return context.WithValue(ctx, checkerKey, c)
}

// FromContext retrieves the Checker from the context.
// Returns nil if no Checker is stored — callers must handle nil (fail-closed).
func FromContext(ctx context.Context) Checker {
	c, _ := ctx.Value(checkerKey).(Checker)
	return c
}

// --- path helpers ---

// Canonicalize resolves symlinks on the deepest existing ancestor of path,
// appends remaining segments, and returns a clean absolute path.
// This is the single source of truth for path canonicalization in the codebase.
func Canonicalize(path string) (string, error) {
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("cannot make path absolute: %w", err)
		}
		path = abs
	}

	// Fast path: full EvalSymlinks for existing paths.
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(resolved), nil
	}

	// Walk up to find the deepest existing ancestor.
	clean := filepath.Clean(path)
	var tail []string
	current := clean
	for {
		resolved, err = filepath.EvalSymlinks(current)
		if err == nil {
			parts := append([]string{resolved}, tail...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(clean), nil
		}
		tail = append([]string{filepath.Base(current)}, tail...)
		current = parent
	}
}

// isContainedIn checks if path is equal to or under root.
// Both must be canonicalized.
func isContainedIn(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..")
}
