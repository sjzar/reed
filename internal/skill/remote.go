package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	gitTimeout       = 120 * time.Second
	httpTimeout      = 30 * time.Second
	httpMaxBytes     = 10 << 20 // 10 MB
	manifestFileName = "remote-skills.json"
	maxDiscoverDepth = 5
)

var commitHashRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// discoverSkipDirs lists directories to skip during skill discovery.
var discoverSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".github":      true,
	"testdata":     true,
}

// manifest tracks remote skill clones for cache freshness.
type manifest struct {
	Entries map[string]*manifestEntry `json:"entries"`
}

type manifestEntry struct {
	CloneURL  string    `json:"clone_url"`
	Ref       string    `json:"ref"`
	Commit    string    `json:"commit"`
	Dir       string    `json:"dir"`
	FetchedAt time.Time `json:"fetched_at"`
}

// ClassifyUses determines if a uses string refers to a remote source.
// Returns false for local paths, true with a RemoteRef for remote sources.
func ClassifyUses(uses string) (bool, *RemoteRef, error) {
	// 1. Explicit relative path → local
	if strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "../") {
		return false, nil, nil
	}

	// 2. Contains github.com → GitHub
	if strings.Contains(uses, "github.com") {
		// Check for blob/tree URL pattern (directory or file view)
		if strings.Contains(uses, "/blob/") || strings.Contains(uses, "/tree/") {
			ref, err := parseGitHubPathURL(uses)
			if err != nil {
				return false, nil, err
			}
			return true, ref, nil
		}
		ref, err := parseGitHubShorthand(uses)
		if err != nil {
			return false, nil, err
		}
		return true, ref, nil
	}

	// 3. Starts with github/ → shorthand without .com
	if strings.HasPrefix(uses, "github/") {
		ref, err := parseGitHubShorthand(uses)
		if err != nil {
			return false, nil, err
		}
		return true, ref, nil
	}

	// 4-5. HTTPS URL (plaintext http:// is rejected for security)
	if strings.HasPrefix(uses, "https://") {
		if !strings.HasSuffix(uses, "/"+skillFileName) {
			return false, nil, fmt.Errorf("remote HTTP skill URL must end with /%s, got %q", skillFileName, uses)
		}
		return true, &RemoteRef{
			Kind:   RemoteHTTP,
			RawURL: uses,
		}, nil
	}
	if strings.HasPrefix(uses, "http://") {
		return false, nil, fmt.Errorf("plaintext http:// not allowed for remote skills, use https:// instead: %q", uses)
	}

	// 6. Everything else → local
	return false, nil, nil
}

// parseGitHubShorthand parses GitHub shorthand into a RemoteRef.
// Accepts: github.com/user/repo[@ref], github.com/user/repo/path[@ref], github/user/repo[@ref]
func parseGitHubShorthand(uses string) (*RemoteRef, error) {
	// Strip https:// if present
	s := strings.TrimPrefix(uses, "https://")

	// Normalize github/ → github.com/
	if after, ok := strings.CutPrefix(s, "github/"); ok {
		s = "github.com/" + after
	}

	// Split on @ for ref
	ref := "main"
	if idx := strings.LastIndex(s, "@"); idx != -1 {
		ref = s[idx+1:]
		s = s[:idx]
		if ref == "" {
			return nil, fmt.Errorf("empty ref after @ in %q", uses)
		}
	}

	// Split by /
	parts := strings.Split(s, "/")
	if len(parts) < 3 {
		return nil, fmt.Errorf("GitHub shorthand requires at least github.com/user/repo, got %q", uses)
	}
	if parts[0] != "github.com" {
		return nil, fmt.Errorf("unexpected host %q in GitHub shorthand %q", parts[0], uses)
	}

	user := parts[1]
	repo := parts[2]
	if user == "" || repo == "" {
		return nil, fmt.Errorf("empty user or repo in GitHub shorthand %q", uses)
	}

	// SubPath from remaining segments
	var subPath string
	if len(parts) > 3 {
		subPath = strings.Join(parts[3:], "/")
		if containsDotDotSegment(subPath) {
			return nil, fmt.Errorf("path traversal not allowed in subpath %q", subPath)
		}
	}

	return &RemoteRef{
		Kind:     RemoteGitHub,
		CloneURL: fmt.Sprintf("https://github.com/%s/%s.git", user, repo),
		Ref:      ref,
		SubPath:  subPath,
	}, nil
}

// parseGitHubPathURL parses a GitHub blob or tree URL into a RemoteRef.
// Format: https://github.com/user/repo/{blob,tree}/<ref>/<path>
func parseGitHubPathURL(uses string) (*RemoteRef, error) {
	u, err := url.Parse(uses)
	if err != nil {
		return nil, fmt.Errorf("invalid GitHub URL %q: %w", uses, err)
	}

	// Path: /user/repo/{blob,tree}/<ref>/<path...>
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 5)
	// parts[0]=user, parts[1]=repo, parts[2]="blob"|"tree", parts[3]=ref, parts[4]=path
	if len(parts) < 4 || (parts[2] != "blob" && parts[2] != "tree") {
		return nil, fmt.Errorf("invalid GitHub URL format %q: expected /user/repo/{blob,tree}/ref/path", uses)
	}

	user := parts[0]
	repo := parts[1]
	ref := parts[3]

	var subPath string
	if len(parts) > 4 {
		subPath = parts[4]
		// Strip trailing SKILL.md — we want the directory
		subPath = strings.TrimSuffix(subPath, "/"+skillFileName)
		subPath = strings.TrimSuffix(subPath, skillFileName)
		// Clean up trailing slash
		subPath = strings.TrimSuffix(subPath, "/")
	}

	if containsDotDotSegment(subPath) {
		return nil, fmt.Errorf("path traversal not allowed in subpath %q", subPath)
	}

	return &RemoteRef{
		Kind:     RemoteGitHub,
		CloneURL: fmt.Sprintf("https://github.com/%s/%s.git", user, repo),
		Ref:      ref,
		SubPath:  subPath,
	}, nil
}

// ensureCloned ensures a git repo is cloned and up-to-date in the mod cache.
// Returns the path to the cloned directory.
func ensureCloned(ctx context.Context, ref *RemoteRef, modDir string) (string, error) {
	if err := os.MkdirAll(modDir, 0755); err != nil {
		return "", fmt.Errorf("create mod dir: %w", err)
	}

	cacheKey := buildCacheKey(ref.CloneURL)
	mf, err := readManifest(modDir)
	if err != nil {
		return "", err
	}

	manifestKey := ref.CloneURL + "@" + ref.Ref
	entry := mf.Entries[manifestKey]

	// Resolve remote commit hash
	remoteHash, effectiveRef, err := resolveRemoteHash(ctx, ref)
	if err != nil {
		// Network failure: fall back to cached if available
		if entry != nil {
			cachedDir := filepath.Join(modDir, entry.Dir)
			if _, statErr := os.Stat(cachedDir); statErr == nil {
				return cachedDir, nil
			}
		}
		return "", fmt.Errorf("resolve remote ref %q: %w", ref.Ref, err)
	}

	// Cache hit: commit matches
	if entry != nil && entry.Commit == remoteHash {
		cachedDir := filepath.Join(modDir, entry.Dir)
		if _, statErr := os.Stat(cachedDir); statErr == nil {
			return cachedDir, nil
		}
		// Directory missing despite manifest entry — re-fetch below
	}

	// Clone into temporary directory
	gitCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	tmpDir, err := os.MkdirTemp(modDir, cacheKey+"_tmp_")
	if err != nil {
		return "", fmt.Errorf("create temp dir for clone: %w", err)
	}
	defer func() {
		// Clean up tmpDir if it still exists (failure path)
		os.RemoveAll(tmpDir)
	}()

	gitEnv := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	// Robust fetch: init → remote add → fetch → checkout
	cmds := []struct {
		args []string
		dir  string
	}{
		{[]string{"init", tmpDir}, ""},
		{[]string{"-C", tmpDir, "remote", "add", "origin", ref.CloneURL}, ""},
		{[]string{"-C", tmpDir, "fetch", "--depth", "1", "origin", effectiveRef}, ""},
		{[]string{"-C", tmpDir, "checkout", "FETCH_HEAD"}, ""},
	}

	for _, c := range cmds {
		cmd := exec.CommandContext(gitCtx, "git", c.args...)
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			if gitCtx.Err() == context.DeadlineExceeded {
				return "", fmt.Errorf("git fetch timed out after %s", gitTimeout)
			}
			return "", fmt.Errorf("git %s: %s: %w", strings.Join(c.args, " "), strings.TrimSpace(string(out)), err)
		}
	}

	// Get the resolved commit hash
	hashCmd := exec.CommandContext(gitCtx, "git", "-C", tmpDir, "rev-parse", "HEAD")
	hashCmd.Env = gitEnv
	hashOut, err := hashCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	commitHash := strings.TrimSpace(string(hashOut))

	// Move to final location
	dirName := fmt.Sprintf("%s_%s", cacheKey, commitHash[:12])
	finalDir := filepath.Join(modDir, dirName)

	if err := os.Rename(tmpDir, finalDir); err != nil {
		// Concurrent race: another process may have won
		if _, statErr := os.Stat(finalDir); statErr == nil {
			return finalDir, nil
		}
		return "", fmt.Errorf("rename clone to cache: %w", err)
	}

	// Update manifest
	mf.Entries[manifestKey] = &manifestEntry{
		CloneURL:  ref.CloneURL,
		Ref:       ref.Ref,
		Commit:    commitHash,
		Dir:       dirName,
		FetchedAt: time.Now().UTC(),
	}
	if err := writeManifest(modDir, mf); err != nil {
		// Non-fatal: clone succeeded
		return finalDir, nil
	}

	return finalDir, nil
}

// resolveRemoteHash resolves a git ref to a commit hash using ls-remote.
// For pinned 40-char hex commits, it returns the hash directly without network.
// Returns the resolved hash and the effective ref (may differ for main→master fallback).
func resolveRemoteHash(ctx context.Context, ref *RemoteRef) (string, string, error) {
	// Pinned commit: skip ls-remote
	if commitHashRe.MatchString(ref.Ref) {
		return ref.Ref, ref.Ref, nil
	}

	gitCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	effectiveRef := ref.Ref
	hash, err := lsRemote(gitCtx, ref.CloneURL, effectiveRef)
	if err != nil {
		return "", "", err
	}

	// main→master fallback
	if hash == "" && effectiveRef == "main" {
		hash, err = lsRemote(gitCtx, ref.CloneURL, "master")
		if err != nil {
			return "", "", err
		}
		if hash != "" {
			effectiveRef = "master"
		}
	}

	if hash == "" {
		return "", "", fmt.Errorf("ref %q not found in remote %s", ref.Ref, ref.CloneURL)
	}

	return hash, effectiveRef, nil
}

// lsRemote runs git ls-remote and returns the hash for the given ref, or "" if not found.
func lsRemote(ctx context.Context, cloneURL, ref string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", cloneURL, ref)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git ls-remote timed out after %s", gitTimeout)
		}
		return "", fmt.Errorf("git ls-remote %s %s: %w", cloneURL, ref, err)
	}
	// Output format: "<hash>\t<ref>\n"
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", nil
	}
	parts := strings.Fields(line)
	if len(parts) >= 1 {
		return parts[0], nil
	}
	return "", nil
}

// discoverSkills finds all skill directories within a cloned repo.
// Priority: checks <dir>/skills/ first; falls back to full walk with depth/skip limits.
func discoverSkills(cloneDir string) ([]string, error) {
	// Priority 1: check skills/ subdirectory
	skillsDir := filepath.Join(cloneDir, "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		var found []string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			skillPath := filepath.Join(skillsDir, e.Name(), skillFileName)
			if _, err := os.Stat(skillPath); err == nil {
				found = append(found, filepath.Join(skillsDir, e.Name()))
			}
		}
		if len(found) > 0 {
			return found, nil
		}
	}

	// Priority 2: walk entire tree with safety limits
	var found []string
	baseParts := len(strings.Split(filepath.Clean(cloneDir), string(filepath.Separator)))

	err := filepath.WalkDir(cloneDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}

		// Depth check
		pathParts := len(strings.Split(filepath.Clean(path), string(filepath.Separator)))
		depth := pathParts - baseParts
		if depth > maxDiscoverDepth {
			return fs.SkipDir
		}

		// Skip known non-skill directories
		if discoverSkipDirs[d.Name()] {
			return fs.SkipDir
		}

		// Check for SKILL.md
		skillPath := filepath.Join(path, skillFileName)
		if _, err := os.Stat(skillPath); err == nil {
			found = append(found, path)
			return fs.SkipDir // Don't recurse into skill directories
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover skills in %s: %w", cloneDir, err)
	}

	if len(found) == 0 {
		return nil, fmt.Errorf("no skills found in %s", cloneDir)
	}

	// Warn about external dependencies (logged but not returned as error)
	for _, dir := range found {
		for _, depFile := range []string{"requirements.txt", "package.json"} {
			if _, err := os.Stat(filepath.Join(dir, depFile)); err == nil {
				// TODO: log warning about external dependencies
				break
			}
		}
	}

	return found, nil
}

// resolveRemote resolves a remote skill reference into one or more ResolvedSkills.
// GitHub without SubPath discovers multiple skills (1:N fan-out).
// GitHub with SubPath or HTTP resolves to a single skill (1:1).
func resolveRemote(ctx context.Context, id string, ref *RemoteRef, modDir string) (map[string]*ResolvedSkill, error) {
	if err := ValidateRemoteRef(ref); err != nil {
		return nil, fmt.Errorf("skill %q: %w", id, err)
	}

	switch ref.Kind {
	case RemoteHTTP:
		sk, err := resolveHTTPSkill(ctx, id, ref, modDir)
		if err != nil {
			return nil, err
		}
		return map[string]*ResolvedSkill{id: sk}, nil

	case RemoteGitHub:
		cloneDir, err := ensureCloned(ctx, ref, modDir)
		if err != nil {
			return nil, fmt.Errorf("skill %q: %w", id, err)
		}

		if ref.SubPath != "" {
			// 1:1 — specific path
			skillDir := filepath.Join(cloneDir, ref.SubPath)
			// Resolve symlinks to get real paths, preventing symlink escape
			realSkillDir, err := filepath.EvalSymlinks(skillDir)
			if err != nil {
				return nil, fmt.Errorf("skill %q: resolve subpath: %w", id, err)
			}
			realCloneDir, err := filepath.EvalSymlinks(cloneDir)
			if err != nil {
				return nil, fmt.Errorf("skill %q: resolve clone dir: %w", id, err)
			}
			if !strings.HasPrefix(realSkillDir, realCloneDir+string(filepath.Separator)) {
				return nil, fmt.Errorf("skill %q: subpath escapes clone directory", id)
			}

			sk, err := scanDirRaw(skillDir)
			if err != nil {
				return nil, fmt.Errorf("skill %q: %w", id, err)
			}
			sk.ID = id
			sk.Source = SkillSource{Kind: SourceRemote, Locator: ref.CloneURL + "@" + ref.Ref + "/" + ref.SubPath, SourceDir: skillDir}
			return map[string]*ResolvedSkill{id: sk}, nil
		}

		// 1:N — discover all skills in repo
		dirs, err := discoverSkills(cloneDir)
		if err != nil {
			return nil, fmt.Errorf("skill %q: %w", id, err)
		}

		result := make(map[string]*ResolvedSkill, len(dirs))
		seen := make(map[string]bool, len(dirs))

		for _, dir := range dirs {
			sk, err := scanDirRaw(dir)
			if err != nil {
				return nil, fmt.Errorf("skill %q: scan %s: %w", id, dir, err)
			}

			// Check for duplicate meta names within the repo
			if seen[sk.Meta.Name] {
				return nil, fmt.Errorf("skill %q: duplicate skill name %q found in repo", id, sk.Meta.Name)
			}
			seen[sk.Meta.Name] = true

			catalogKey := id + "." + sk.Meta.Name
			sk.ID = catalogKey
			sk.Source = SkillSource{Kind: SourceRemote, Locator: ref.CloneURL + "@" + ref.Ref, SourceDir: dir}
			result[catalogKey] = sk
		}

		return result, nil

	default:
		return nil, fmt.Errorf("skill %q: unsupported remote kind %q", id, ref.Kind)
	}
}

// resolveHTTPSkill downloads and resolves a single skill from an HTTP URL.
func resolveHTTPSkill(ctx context.Context, id string, ref *RemoteRef, modDir string) (*ResolvedSkill, error) {
	if err := os.MkdirAll(modDir, 0755); err != nil {
		return nil, fmt.Errorf("skill %q: create mod dir: %w", id, err)
	}

	httpCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodGet, ref.RawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("skill %q: create request: %w", id, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("skill %q: fetch %s: %w", id, ref.RawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skill %q: fetch %s: HTTP %d", id, ref.RawURL, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("skill %q: read response: %w", id, err)
	}
	if len(data) > httpMaxBytes {
		return nil, fmt.Errorf("skill %q: response exceeds %d byte limit", id, httpMaxBytes)
	}

	meta, body, err := ParseFrontMatter(data)
	if err != nil {
		return nil, fmt.Errorf("skill %q: parse %s: %w", id, ref.RawURL, err)
	}

	if err := ValidateMeta(meta); err != nil {
		return nil, fmt.Errorf("skill %q: %w", id, err)
	}

	files := []ResolvedFile{{Path: skillFileName, Content: data}}
	digest := ComputeDigest(files)

	// Content-addressed cache
	h := sha256.Sum256(data)
	contentHash := hex.EncodeToString(h[:])
	dirName := fmt.Sprintf("%s_%s", id, contentHash[:12])
	finalDir := filepath.Join(modDir, dirName)

	if _, err := os.Stat(finalDir); err != nil {
		tmpDir, err := os.MkdirTemp(modDir, id+"_http_tmp_")
		if err != nil {
			return nil, fmt.Errorf("skill %q: create temp dir: %w", id, err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, skillFileName), data, 0644); err != nil {
			os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("skill %q: write %s: %w", id, skillFileName, err)
		}
		if err := os.Rename(tmpDir, finalDir); err != nil {
			os.RemoveAll(tmpDir)
			if _, statErr := os.Stat(finalDir); statErr != nil {
				return nil, fmt.Errorf("skill %q: materialize to cache: %w", id, err)
			}
		}
	}

	return &ResolvedSkill{
		ID:         id,
		Meta:       meta,
		Body:       body,
		Files:      files,
		Source:     SkillSource{Kind: SourceRemote, Locator: ref.RawURL, SourceDir: finalDir},
		Backing:    BackingMod,
		BackingDir: finalDir,
		Digest:     digest,
	}, nil
}

// DeriveIDFromUses generates a catalog-safe ID from a remote uses string.
// For GitHub: last path segment before @version (e.g., "claude-api" from ".../skills/claude-api@v1").
// For HTTP: filename stem (e.g., "my-skill" from "https://.../my-skill/SKILL.md").
// Falls back to a hash-based ID if derivation fails.
func DeriveIDFromUses(uses string) string {
	// Strip @version suffix
	s := uses
	if idx := strings.LastIndex(s, "@"); idx != -1 {
		s = s[:idx]
	}

	// Strip trailing SKILL.md
	s = strings.TrimSuffix(s, "/"+skillFileName)
	s = strings.TrimSuffix(s, skillFileName)

	// Strip trailing slash
	s = strings.TrimSuffix(s, "/")

	// Take last path segment
	if idx := strings.LastIndex(s, "/"); idx != -1 {
		s = s[idx+1:]
	}

	// Validate as skill name
	if s != "" && validSkillNameRe.MatchString(s) {
		return s
	}

	// Fallback: hash-based ID
	h := sha256.Sum256([]byte(uses))
	return "remote-" + hex.EncodeToString(h[:6])
}

// buildCacheKey converts a clone URL into a filesystem-safe cache key.
func buildCacheKey(cloneURL string) string {
	s := strings.TrimPrefix(cloneURL, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimSuffix(s, ".git")
	s = strings.ReplaceAll(s, "/", "_")
	return s
}

// readManifest reads the remote skills manifest from modDir.
func readManifest(modDir string) (*manifest, error) {
	path := filepath.Join(modDir, manifestFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &manifest{Entries: make(map[string]*manifestEntry)}, nil
		}
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var mf manifest
	if err := json.Unmarshal(data, &mf); err != nil {
		// Corrupt manifest: start fresh
		return &manifest{Entries: make(map[string]*manifestEntry)}, nil
	}
	if mf.Entries == nil {
		mf.Entries = make(map[string]*manifestEntry)
	}
	return &mf, nil
}

// writeManifest atomically writes the remote skills manifest to modDir.
func writeManifest(modDir string, mf *manifest) error {
	path := filepath.Join(modDir, manifestFileName)
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	tmp, err := os.CreateTemp(modDir, manifestFileName+"_tmp_")
	if err != nil {
		return fmt.Errorf("create temp manifest: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp manifest: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename manifest: %w", err)
	}
	return nil
}
