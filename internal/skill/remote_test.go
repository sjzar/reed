package skill

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestClassifyUses(t *testing.T) {
	tests := []struct {
		name     string
		uses     string
		isRemote bool
		kind     RemoteKind
		wantErr  bool
	}{
		// Local paths
		{name: "relative dir", uses: "./skills/review", isRemote: false},
		{name: "parent dir", uses: "../shared/skill", isRemote: false},
		{name: "bare path", uses: "skills/my-skill", isRemote: false},

		// GitHub shorthand
		{name: "github.com basic", uses: "github.com/user/repo@v1.0", isRemote: true, kind: RemoteGitHub},
		{name: "github.com with path", uses: "github.com/user/repo/skills/review@v1.0", isRemote: true, kind: RemoteGitHub},
		{name: "github.com no version", uses: "github.com/user/repo", isRemote: true, kind: RemoteGitHub},
		{name: "github shorthand", uses: "github/user/repo@v1.0", isRemote: true, kind: RemoteGitHub},
		{name: "github.com with https", uses: "https://github.com/user/repo@v1.0", isRemote: true, kind: RemoteGitHub},

		// GitHub blob/tree URL
		{name: "github blob URL", uses: "https://github.com/user/repo/blob/main/skills/review/SKILL.md", isRemote: true, kind: RemoteGitHub},
		{name: "github tree URL", uses: "https://github.com/user/repo/tree/main/skills/claude-api", isRemote: true, kind: RemoteGitHub},

		// HTTP
		{name: "https skill", uses: "https://example.com/path/to/SKILL.md", isRemote: true, kind: RemoteHTTP},

		// HTTP errors
		{name: "https no skill.md", uses: "https://example.com/path/to/other.md", wantErr: true},
		{name: "plaintext http rejected", uses: "http://example.com/path/to/SKILL.md", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isRemote, ref, err := ClassifyUses(tt.uses)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if isRemote != tt.isRemote {
				t.Errorf("isRemote: got %v, want %v", isRemote, tt.isRemote)
			}
			if isRemote && ref.Kind != tt.kind {
				t.Errorf("kind: got %v, want %v", ref.Kind, tt.kind)
			}
		})
	}
}

func TestParseGitHubShorthand(t *testing.T) {
	tests := []struct {
		name     string
		uses     string
		cloneURL string
		ref      string
		subPath  string
		wantErr  bool
	}{
		{
			name:     "basic with tag",
			uses:     "github.com/user/repo@v1.0",
			cloneURL: "https://github.com/user/repo.git",
			ref:      "v1.0",
		},
		{
			name:     "no version defaults main",
			uses:     "github.com/user/repo",
			cloneURL: "https://github.com/user/repo.git",
			ref:      "main",
		},
		{
			name:     "with subpath",
			uses:     "github.com/user/repo/skills/review@v2.0",
			cloneURL: "https://github.com/user/repo.git",
			ref:      "v2.0",
			subPath:  "skills/review",
		},
		{
			name:     "github shorthand without .com",
			uses:     "github/user/repo@v1.0",
			cloneURL: "https://github.com/user/repo.git",
			ref:      "v1.0",
		},
		{
			name:     "with https prefix",
			uses:     "https://github.com/user/repo@v1.0",
			cloneURL: "https://github.com/user/repo.git",
			ref:      "v1.0",
		},
		{
			name:     "commit hash as ref",
			uses:     "github.com/user/repo@abc123def456abc123def456abc123def456abcd",
			cloneURL: "https://github.com/user/repo.git",
			ref:      "abc123def456abc123def456abc123def456abcd",
		},
		{name: "empty ref after @", uses: "github.com/user/repo@", wantErr: true},
		{name: "too few segments", uses: "github.com/user", wantErr: true},
		{name: "path traversal", uses: "github.com/user/repo/../etc@v1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := parseGitHubShorthand(tt.uses)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ref.CloneURL != tt.cloneURL {
				t.Errorf("cloneURL: got %q, want %q", ref.CloneURL, tt.cloneURL)
			}
			if ref.Ref != tt.ref {
				t.Errorf("ref: got %q, want %q", ref.Ref, tt.ref)
			}
			if ref.SubPath != tt.subPath {
				t.Errorf("subPath: got %q, want %q", ref.SubPath, tt.subPath)
			}
		})
	}
}

func TestParseGitHubPathURL(t *testing.T) {
	tests := []struct {
		name     string
		uses     string
		cloneURL string
		ref      string
		subPath  string
		wantErr  bool
	}{
		{
			name:     "standard blob URL",
			uses:     "https://github.com/user/repo/blob/main/skills/review/SKILL.md",
			cloneURL: "https://github.com/user/repo.git",
			ref:      "main",
			subPath:  "skills/review",
		},
		{
			name:     "blob URL with tag ref",
			uses:     "https://github.com/user/repo/blob/v1.0/my-skill/SKILL.md",
			cloneURL: "https://github.com/user/repo.git",
			ref:      "v1.0",
			subPath:  "my-skill",
		},
		{
			name:     "blob URL root SKILL.md",
			uses:     "https://github.com/user/repo/blob/main/SKILL.md",
			cloneURL: "https://github.com/user/repo.git",
			ref:      "main",
			subPath:  "",
		},
		{
			name:     "tree URL directory",
			uses:     "https://github.com/user/repo/tree/main/skills/claude-api",
			cloneURL: "https://github.com/user/repo.git",
			ref:      "main",
			subPath:  "skills/claude-api",
		},
		{
			name:     "tree URL root",
			uses:     "https://github.com/user/repo/tree/v2.0",
			cloneURL: "https://github.com/user/repo.git",
			ref:      "v2.0",
			subPath:  "",
		},
		{
			name:    "invalid path component",
			uses:    "https://github.com/user/repo/commits/main/path",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := parseGitHubPathURL(tt.uses)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ref.CloneURL != tt.cloneURL {
				t.Errorf("cloneURL: got %q, want %q", ref.CloneURL, tt.cloneURL)
			}
			if ref.Ref != tt.ref {
				t.Errorf("ref: got %q, want %q", ref.Ref, tt.ref)
			}
			if ref.SubPath != tt.subPath {
				t.Errorf("subPath: got %q, want %q", ref.SubPath, tt.subPath)
			}
		})
	}
}

func TestDiscoverSkills(t *testing.T) {
	t.Run("skills/ directory priority", func(t *testing.T) {
		dir := t.TempDir()
		// Create skills/ with two skills
		for _, name := range []string{"review", "search"} {
			skillDir := filepath.Join(dir, "skills", name)
			if err := os.MkdirAll(skillDir, 0755); err != nil {
				t.Fatal(err)
			}
			content := fmt.Sprintf("---\nname: %s\ndescription: %s skill\n---\nBody\n", name, name)
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}
		// Also create a SKILL.md outside skills/ — should be ignored because skills/ has results
		otherDir := filepath.Join(dir, "extra", "tool")
		if err := os.MkdirAll(otherDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(otherDir, "SKILL.md"), []byte("---\nname: tool\ndescription: Tool\n---\n"), 0644); err != nil {
			t.Fatal(err)
		}

		found, err := discoverSkills(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(found) != 2 {
			t.Fatalf("expected 2 skills, got %d: %v", len(found), found)
		}
	})

	t.Run("fallback walk", func(t *testing.T) {
		dir := t.TempDir()
		// No skills/ dir — create skills at various locations
		for _, path := range []string{"tools/lint", "tools/format"} {
			skillDir := filepath.Join(dir, path)
			if err := os.MkdirAll(skillDir, 0755); err != nil {
				t.Fatal(err)
			}
			name := filepath.Base(path)
			content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n", name, name)
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}

		found, err := discoverSkills(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(found) != 2 {
			t.Fatalf("expected 2 skills, got %d: %v", len(found), found)
		}
	})

	t.Run("skip .git and node_modules", func(t *testing.T) {
		dir := t.TempDir()
		// Create skills in skip-listed directories
		for _, skipDir := range []string{".git", "node_modules"} {
			skillDir := filepath.Join(dir, skipDir, "hidden-skill")
			if err := os.MkdirAll(skillDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: hidden\ndescription: Hidden\n---\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}

		_, err := discoverSkills(dir)
		if err == nil {
			t.Fatal("expected error (no skills found), got nil")
		}
		if !strings.Contains(err.Error(), "no skills found") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("depth limit", func(t *testing.T) {
		dir := t.TempDir()
		// Create a skill at depth 6 — should not be found
		deep := filepath.Join(dir, "a", "b", "c", "d", "e", "f", "my-skill")
		if err := os.MkdirAll(deep, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(deep, "SKILL.md"), []byte("---\nname: deep\ndescription: Deep\n---\n"), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := discoverSkills(dir)
		if err == nil {
			t.Fatal("expected error (no skills found), got nil")
		}
	})

	t.Run("no skills", func(t *testing.T) {
		dir := t.TempDir()
		_, err := discoverSkills(dir)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

// createBareRepo creates a bare git repo with a skill in t.TempDir and returns the path.
func createBareRepo(t *testing.T, skills map[string]string) string {
	t.Helper()

	// Check if git is available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	workDir := t.TempDir()
	bareDir := t.TempDir()

	// Init bare repo
	run(t, workDir, "git", "init", "--bare", bareDir)

	// Init work repo and add files
	run(t, workDir, "git", "init")
	run(t, workDir, "git", "remote", "add", "origin", bareDir)
	run(t, workDir, "git", "config", "user.email", "test@test.com")
	run(t, workDir, "git", "config", "user.name", "Test")

	for path, content := range skills {
		fullPath := filepath.Join(workDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	run(t, workDir, "git", "add", "-A")
	run(t, workDir, "git", "commit", "-m", "initial")
	run(t, workDir, "git", "push", "origin", "HEAD:main")

	return bareDir
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %s: %v", name, args, string(out), err)
	}
}

func TestEnsureCloned(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	bareDir := createBareRepo(t, map[string]string{
		"skills/review/SKILL.md": "---\nname: review\ndescription: Review skill\n---\nReview body\n",
	})

	modDir := t.TempDir()
	ctx := context.Background()

	ref := &RemoteRef{
		Kind:     RemoteGitHub,
		CloneURL: bareDir,
		Ref:      "main",
	}

	t.Run("fresh clone", func(t *testing.T) {
		cloneDir, err := ensureCloned(ctx, ref, modDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify SKILL.md exists
		skillPath := filepath.Join(cloneDir, "skills", "review", "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			t.Fatalf("SKILL.md not found at %s: %v", skillPath, err)
		}
	})

	t.Run("cache hit", func(t *testing.T) {
		// Second call should return cached dir
		cloneDir, err := ensureCloned(ctx, ref, modDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cloneDir == "" {
			t.Fatal("expected non-empty clone dir")
		}
	})
}

func TestHTTPSkill(t *testing.T) {
	skillContent := "---\nname: http-skill\ndescription: HTTP skill\n---\nHTTP body\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/path/to/SKILL.md" {
			w.Write([]byte(skillContent))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	modDir := t.TempDir()
	ctx := context.Background()

	ref := &RemoteRef{
		Kind:   RemoteHTTP,
		RawURL: srv.URL + "/path/to/SKILL.md",
	}

	result, err := resolveRemote(ctx, "http-skill", ref, modDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sk, ok := result["http-skill"]
	if !ok {
		t.Fatal("expected skill 'http-skill' in result")
	}
	if sk.Meta.Name != "http-skill" {
		t.Errorf("name: got %q, want %q", sk.Meta.Name, "http-skill")
	}
	if sk.Body != "HTTP body\n" {
		t.Errorf("body: got %q, want %q", sk.Body, "HTTP body\n")
	}
	if sk.Backing != BackingMod {
		t.Errorf("backing: got %q, want %q", sk.Backing, BackingMod)
	}
}

func TestHTTPSkill_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	modDir := t.TempDir()
	ctx := context.Background()

	ref := &RemoteRef{
		Kind:   RemoteHTTP,
		RawURL: srv.URL + "/missing/SKILL.md",
	}

	_, err := resolveRemote(ctx, "missing", ref, modDir)
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 in error, got: %v", err)
	}
}

func TestHTTPSkill_TooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write more than 10MB
		data := make([]byte, httpMaxBytes+100)
		w.Write(data)
	}))
	defer srv.Close()

	modDir := t.TempDir()
	ctx := context.Background()

	ref := &RemoteRef{
		Kind:   RemoteHTTP,
		RawURL: srv.URL + "/big/SKILL.md",
	}

	_, err := resolveRemote(ctx, "big", ref, modDir)
	if err == nil {
		t.Fatal("expected error for oversized response, got nil")
	}
}

func TestNamespace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	bareDir := createBareRepo(t, map[string]string{
		"skills/review/SKILL.md":      "---\nname: review\ndescription: Review skill\n---\n",
		"skills/code-search/SKILL.md": "---\nname: code-search\ndescription: Code search skill\n---\n",
	})

	modDir := t.TempDir()
	ctx := context.Background()

	ref := &RemoteRef{
		Kind:     RemoteGitHub,
		CloneURL: bareDir,
		Ref:      "main",
	}

	t.Run("fan-out produces namespaced keys", func(t *testing.T) {
		result, err := resolveRemote(ctx, "my-tools", ref, modDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result) != 2 {
			t.Fatalf("expected 2 skills, got %d", len(result))
		}

		if _, ok := result["my-tools.review"]; !ok {
			t.Error("expected key 'my-tools.review'")
		}
		if _, ok := result["my-tools.code-search"]; !ok {
			t.Error("expected key 'my-tools.code-search'")
		}

		// Verify IDs match keys
		for key, sk := range result {
			if sk.ID != key {
				t.Errorf("skill ID %q doesn't match key %q", sk.ID, key)
			}
		}
	})

	t.Run("subpath produces single key", func(t *testing.T) {
		refWithPath := &RemoteRef{
			Kind:     RemoteGitHub,
			CloneURL: bareDir,
			Ref:      "main",
			SubPath:  "skills/review",
		}

		result, err := resolveRemote(ctx, "my-review", refWithPath, modDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("expected 1 skill, got %d", len(result))
		}
		if _, ok := result["my-review"]; !ok {
			t.Error("expected key 'my-review'")
		}
	})
}

func TestNamespace_DuplicateNames(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Two skills with the same meta name under skills/ directory
	bareDir := createBareRepo(t, map[string]string{
		"skills/review-a/SKILL.md": "---\nname: review\ndescription: Review 1\n---\n",
		"skills/review-b/SKILL.md": "---\nname: review\ndescription: Review 2\n---\n",
	})

	modDir := t.TempDir()
	ctx := context.Background()

	ref := &RemoteRef{
		Kind:     RemoteGitHub,
		CloneURL: bareDir,
		Ref:      "main",
	}

	_, err := resolveRemote(ctx, "pack", ref, modDir)
	if err == nil {
		t.Fatal("expected error for duplicate names, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate error, got: %v", err)
	}
}

func TestNamespace_ServiceMutualExclusion(t *testing.T) {
	svc := New(t.TempDir(), t.TempDir())

	// Manually add a namespaced skill to the catalog
	svc.mu.Lock()
	svc.catalog["tools.review"] = &ResolvedSkill{
		ID:   "tools.review",
		Meta: SkillMeta{Name: "review", Description: "Review"},
	}
	svc.mu.Unlock()

	// Try to add a plain skill with the namespace prefix — should fail
	workflowDir := t.TempDir()
	skillDir := filepath.Join(workflowDir, "skills", "tools")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: tools\ndescription: Tools\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}

	specs := map[string]model.SkillSpec{
		"tools": {Uses: "skills/tools"},
	}
	err := svc.LoadWorkflow(context.Background(), specs, workflowDir)
	if err == nil {
		t.Fatal("expected mutual exclusion error, got nil")
	}
	if !strings.Contains(err.Error(), "conflicts") {
		t.Errorf("expected conflict error, got: %v", err)
	}
}

func TestValidateRemoteRef(t *testing.T) {
	tests := []struct {
		name    string
		ref     *RemoteRef
		wantErr bool
	}{
		{name: "nil ref", ref: nil, wantErr: true},
		{name: "valid github", ref: &RemoteRef{Kind: RemoteGitHub, CloneURL: "https://github.com/u/r.git", Ref: "main"}, wantErr: false},
		{name: "github missing clone URL", ref: &RemoteRef{Kind: RemoteGitHub, Ref: "main"}, wantErr: true},
		{name: "github missing ref", ref: &RemoteRef{Kind: RemoteGitHub, CloneURL: "https://github.com/u/r.git"}, wantErr: true},
		{name: "valid http", ref: &RemoteRef{Kind: RemoteHTTP, RawURL: "https://example.com/SKILL.md"}, wantErr: false},
		{name: "http missing URL", ref: &RemoteRef{Kind: RemoteHTTP}, wantErr: true},
		{name: "path traversal", ref: &RemoteRef{Kind: RemoteGitHub, CloneURL: "https://github.com/u/r.git", Ref: "main", SubPath: "skills/../etc"}, wantErr: true},
		{name: "unknown kind", ref: &RemoteRef{Kind: "ftp"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRemoteRef(tt.ref)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRemoteRef() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildCacheKey(t *testing.T) {
	tests := []struct {
		cloneURL string
		want     string
	}{
		{"https://github.com/user/repo.git", "github.com_user_repo"},
		{"https://github.com/org/my-tool.git", "github.com_org_my-tool"},
	}

	for _, tt := range tests {
		got := buildCacheKey(tt.cloneURL)
		if got != tt.want {
			t.Errorf("buildCacheKey(%q) = %q, want %q", tt.cloneURL, got, tt.want)
		}
	}
}

func TestManifestReadWrite(t *testing.T) {
	modDir := t.TempDir()

	// Read non-existent manifest — should return empty
	mf, err := readManifest(modDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mf.Entries) != 0 {
		t.Fatalf("expected empty manifest, got %d entries", len(mf.Entries))
	}

	// Write and read back
	mf.Entries["test@main"] = &manifestEntry{
		CloneURL: "https://github.com/user/repo.git",
		Ref:      "main",
		Commit:   "abc123",
		Dir:      "github.com_user_repo_abc123",
	}

	if err := writeManifest(modDir, mf); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	mf2, err := readManifest(modDir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if len(mf2.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(mf2.Entries))
	}
	entry := mf2.Entries["test@main"]
	if entry.Commit != "abc123" {
		t.Errorf("commit: got %q, want %q", entry.Commit, "abc123")
	}
}

func TestValidateWorkflowSkills_RemoteRef(t *testing.T) {
	workflowDir := t.TempDir()

	specs := map[string]model.SkillSpec{
		"my-tools": {Uses: "github.com/user/repo@v1.0"},
	}

	// Should pass — remote refs are validated for format only, no network
	if err := ValidateWorkflowSkills(specs, workflowDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWorkflowSkills_InvalidRemoteRef(t *testing.T) {
	workflowDir := t.TempDir()

	specs := map[string]model.SkillSpec{
		"bad": {Uses: "https://example.com/not-a-skill.md"},
	}

	err := ValidateWorkflowSkills(specs, workflowDir)
	if err == nil {
		t.Fatal("expected error for invalid HTTP URL, got nil")
	}
}

func TestLoadWorkflow_FanoutNoCollision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Create a repo with a skill named "review"
	bareDir := createBareRepo(t, map[string]string{
		"skills/review/SKILL.md": "---\nname: review\ndescription: Review\n---\n",
	})

	modDir := t.TempDir()
	ctx := context.Background()

	ref := &RemoteRef{
		Kind:     RemoteGitHub,
		CloneURL: bareDir,
		Ref:      "main",
	}

	// Resolve two namespaces from the same repo — should produce distinct keys
	resultA, err := resolveRemote(ctx, "pack-a", ref, modDir)
	if err != nil {
		t.Fatalf("resolve pack-a: %v", err)
	}
	resultB, err := resolveRemote(ctx, "pack-b", ref, modDir)
	if err != nil {
		t.Fatalf("resolve pack-b: %v", err)
	}

	// Verify no key overlap
	for k := range resultA {
		if _, ok := resultB[k]; ok {
			t.Errorf("unexpected key collision: %q exists in both results", k)
		}
	}

	if _, ok := resultA["pack-a.review"]; !ok {
		t.Error("expected key 'pack-a.review'")
	}
	if _, ok := resultB["pack-b.review"]; !ok {
		t.Error("expected key 'pack-b.review'")
	}
}

func TestValidateWorkflowSkills_PlaintextHTTPRejected(t *testing.T) {
	workflowDir := t.TempDir()

	specs := map[string]model.SkillSpec{
		"bad": {Uses: "http://example.com/path/to/SKILL.md"},
	}

	err := ValidateWorkflowSkills(specs, workflowDir)
	if err == nil {
		t.Fatal("expected error for plaintext http, got nil")
	}
	if !strings.Contains(err.Error(), "https://") {
		t.Errorf("expected https suggestion in error, got: %v", err)
	}
}

func TestDeriveIDFromUses(t *testing.T) {
	tests := []struct {
		uses string
		want string
	}{
		{"https://github.com/anthropics/skills/tree/main/skills/claude-api", "claude-api"},
		{"github.com/user/repo/skills/review@v1.0", "review"},
		{"https://example.com/path/to/my-skill/SKILL.md", "my-skill"},
		{"github.com/user/repo@v1.0", "repo"},
		{"https://github.com/user/repo/blob/main/SKILL.md", "main"},
	}

	for _, tt := range tests {
		t.Run(tt.uses, func(t *testing.T) {
			got := DeriveIDFromUses(tt.uses)
			if got != tt.want {
				t.Errorf("DeriveIDFromUses(%q) = %q, want %q", tt.uses, got, tt.want)
			}
		})
	}
}

func TestClassifyUses_TreeURL(t *testing.T) {
	// Verify that tree URLs are recognized as remote GitHub refs
	isRemote, ref, err := ClassifyUses("https://github.com/anthropics/skills/tree/main/skills/claude-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isRemote {
		t.Fatal("expected remote, got local")
	}
	if ref.Kind != RemoteGitHub {
		t.Errorf("kind: got %v, want %v", ref.Kind, RemoteGitHub)
	}
	if ref.Ref != "main" {
		t.Errorf("ref: got %q, want %q", ref.Ref, "main")
	}
	if ref.SubPath != "skills/claude-api" {
		t.Errorf("subPath: got %q, want %q", ref.SubPath, "skills/claude-api")
	}
}
