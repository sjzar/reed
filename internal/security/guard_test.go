package security

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realDir returns the symlink-resolved, canonical form of t.TempDir().
// On macOS, /var → /private/var, so t.TempDir() and canonicalize() disagree.
func realDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestNew_WorkdirProfile(t *testing.T) {
	dir := realDir(t)
	g := New(ProfileWorkdir, dir)

	resolved := filepath.Join(dir, "file.txt")
	if err := g.CheckRead(resolved); err != nil {
		t.Errorf("expected read allowed, got: %v", err)
	}
	if err := g.CheckWrite(resolved); err != nil {
		t.Errorf("expected write allowed, got: %v", err)
	}

	realTmp, _ := filepath.EvalSymlinks(os.TempDir())
	outside := filepath.Join(realTmp, "outside")
	if err := g.CheckRead(outside); err == nil {
		t.Error("expected read denied for path outside cwd")
	}
	if err := g.CheckWrite(outside); err == nil {
		t.Error("expected write denied for path outside cwd")
	}
}

func TestNew_FullProfile(t *testing.T) {
	g := New(ProfileFull, "")

	if err := g.CheckRead("/any/path"); err != nil {
		t.Errorf("full profile should allow read: %v", err)
	}
	if err := g.CheckWrite("/any/path"); err != nil {
		t.Errorf("full profile should allow write: %v", err)
	}
}

func TestGrantRead(t *testing.T) {
	dir := realDir(t)
	extraDir := realDir(t)
	g := New(ProfileWorkdir, dir)

	resolved := filepath.Join(extraDir, "data.txt")
	if err := g.CheckRead(resolved); err == nil {
		t.Error("expected read denied before grant")
	}

	if err := g.GrantRead(extraDir, "test"); err != nil {
		t.Fatalf("GrantRead: %v", err)
	}

	if err := g.CheckRead(resolved); err != nil {
		t.Errorf("expected read allowed after grant: %v", err)
	}

	if err := g.CheckWrite(resolved); err == nil {
		t.Error("expected write denied (only read granted)")
	}
}

func TestGrantWrite(t *testing.T) {
	dir := realDir(t)
	extraDir := realDir(t)
	g := New(ProfileWorkdir, dir)

	if err := g.GrantWrite(extraDir, "test"); err != nil {
		t.Fatalf("GrantWrite: %v", err)
	}

	resolved := filepath.Join(extraDir, "file.txt")
	if err := g.CheckWrite(resolved); err != nil {
		t.Errorf("expected write allowed: %v", err)
	}
	if err := g.CheckRead(resolved); err == nil {
		t.Error("expected read denied (only write granted)")
	}
}

func TestGrant_EmptyRoot(t *testing.T) {
	g := New(ProfileWorkdir, realDir(t))
	if err := g.GrantRead("", "test"); err == nil {
		t.Error("expected error for empty root")
	}
}

func TestGrant_CanonicalizesPath(t *testing.T) {
	base := realDir(t)
	sub := filepath.Join(base, "a", "b")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	g := New(ProfileWorkdir, realDir(t))
	rawPath := filepath.Join(base, "a", "b", "..", "b")
	if err := g.GrantRead(rawPath, "test"); err != nil {
		t.Fatalf("GrantRead: %v", err)
	}

	resolved := filepath.Join(sub, "file.txt")
	if err := g.CheckRead(resolved); err != nil {
		t.Errorf("expected read allowed after canonicalized grant: %v", err)
	}
}

func TestFork_DeepCopy(t *testing.T) {
	dir := realDir(t)
	parent := New(ProfileWorkdir, dir)

	extra := realDir(t)
	if err := parent.GrantRead(extra, "parent-grant"); err != nil {
		t.Fatal(err)
	}

	child := parent.Fork()

	resolved := filepath.Join(extra, "file.txt")
	if err := child.CheckRead(resolved); err != nil {
		t.Errorf("child should inherit parent grant: %v", err)
	}

	newDir := realDir(t)
	if err := child.GrantRead(newDir, "child-grant"); err != nil {
		t.Fatal(err)
	}
	childResolved := filepath.Join(newDir, "file.txt")
	if err := child.CheckRead(childResolved); err != nil {
		t.Errorf("child should access new grant: %v", err)
	}
	if err := parent.CheckRead(childResolved); err == nil {
		t.Error("parent should NOT see child's new grant")
	}
}

func TestFork_PreservesProfile(t *testing.T) {
	g := New(ProfileFull, "")
	child := g.Fork()
	if err := child.CheckRead("/any/path"); err != nil {
		t.Errorf("forked full profile should allow read: %v", err)
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	g := New(ProfileFull, "")

	if FromContext(ctx) != nil {
		t.Error("expected nil before storing")
	}

	ctx = WithChecker(ctx, g)
	checker := FromContext(ctx)
	if checker == nil {
		t.Fatal("expected non-nil after storing")
	}

	if err := checker.CheckRead("/any"); err != nil {
		t.Errorf("checker should work: %v", err)
	}
}

func TestFromContext_NilGuard(t *testing.T) {
	checker := FromContext(context.Background())
	if checker != nil {
		t.Error("expected nil from empty context")
	}
}

func TestAccessDenied_ErrorMessage(t *testing.T) {
	g := New(ProfileWorkdir, realDir(t))
	err := g.CheckRead("/forbidden/path")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("error should contain 'access denied', got: %s", err.Error())
	}
}

func TestSymlinkGrant(t *testing.T) {
	backing := realDir(t)
	if err := os.WriteFile(filepath.Join(backing, "SKILL.md"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	mountParent := realDir(t)
	mountDir := filepath.Join(mountParent, "skill-link")
	if err := os.Symlink(backing, mountDir); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	g := New(ProfileWorkdir, realDir(t))
	if err := g.GrantRead(mountDir, "skill:test"); err != nil {
		t.Fatal(err)
	}
	if err := g.GrantRead(backing, "skill-backing:test"); err != nil {
		t.Fatal(err)
	}

	realPath, err := filepath.EvalSymlinks(filepath.Join(mountDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.CheckRead(realPath); err != nil {
		t.Errorf("should allow read of symlink-resolved path: %v", err)
	}
}

func TestGrants_Snapshot(t *testing.T) {
	g := New(ProfileWorkdir, realDir(t))
	dir := realDir(t)
	if err := g.GrantRead(dir, "extra"); err != nil {
		t.Fatal(err)
	}
	grants := g.Grants()
	if len(grants) < 2 { // cwd + extra
		t.Errorf("expected at least 2 grants, got %d", len(grants))
	}
}

func TestCanonicalize_ExistingPath(t *testing.T) {
	dir := realDir(t)
	result, err := Canonicalize(dir)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if result != dir {
		t.Errorf("expected %s, got %s", dir, result)
	}
}

func TestCanonicalize_NonExistentChild(t *testing.T) {
	dir := realDir(t)
	path := filepath.Join(dir, "does-not-exist", "file.txt")
	result, err := Canonicalize(path)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	expected := filepath.Join(dir, "does-not-exist", "file.txt")
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestCanonicalize_Symlink(t *testing.T) {
	dir := realDir(t)
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	result, err := Canonicalize(link)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if result != target {
		t.Errorf("expected symlink to resolve to %s, got %s", target, result)
	}
}

func TestCanonicalize_DotDot(t *testing.T) {
	dir := realDir(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "a", "b", "..", "b")
	result, err := Canonicalize(path)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if result != sub {
		t.Errorf("expected %s, got %s", sub, result)
	}
}
