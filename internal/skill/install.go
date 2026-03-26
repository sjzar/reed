package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// InstallResult records what was installed by a single Install call.
type InstallResult struct {
	Installed []string // skill names added to the manifest
}

// Install downloads a remote skill source and registers it in the manifest.
// Local paths are rejected — users should copy directly to ./skills/.
func Install(ctx context.Context, source string, scope SkillScope, workDir, homeDir, modDir string) (*InstallResult, error) {
	isRemote, ref, err := ClassifyUses(source)
	if err != nil {
		return nil, err
	}
	if !isRemote {
		return nil, fmt.Errorf("reed skill install only supports remote sources (GitHub, HTTPS).\n\nTo add a local skill, copy it directly:\n  cp -r /path/to/skill ./skills/          # project scope (highest priority)")
	}

	id := DeriveIDFromUses(source)

	resolved, err := resolveRemote(ctx, id, ref, modDir)
	if err != nil {
		return nil, fmt.Errorf("download skill: %w", err)
	}

	manifestPath := ManifestPath(scope, workDir, homeDir)
	if manifestPath == "" {
		return nil, fmt.Errorf("cannot determine manifest path for scope %q", scope)
	}

	mf, err := ReadManifest(manifestPath)
	if err != nil {
		return nil, err
	}

	// Determine commit hash from the remote-skills.json mod manifest
	commitHash := resolveCommitFromModManifest(modDir, ref)

	var installed []string
	for key, sk := range resolved {
		// Compute mod_path relative to modDir
		modPath, err := makeModPath(modDir, sk.BackingDir)
		if err != nil {
			return nil, fmt.Errorf("compute mod_path for %q: %w", key, err)
		}

		// Derive per-skill SubPath from SourceDir relative to the clone root.
		// For fan-out repos (multiple skills), this lets tryRestore resolve the exact skill.
		subPath := deriveSubPath(sk, ref)

		// Use the skill's meta name as the manifest key
		name := sk.Meta.Name

		mf.Skills[name] = &ManifestEntry{
			ModPath:     modPath,
			Source:      source,
			Ref:         refString(ref),
			Commit:      commitHash,
			SubPath:     subPath,
			InstalledAt: time.Now().UTC(),
		}
		installed = append(installed, name)
	}

	if err := WriteManifest(manifestPath, mf); err != nil {
		return nil, err
	}

	return &InstallResult{Installed: installed}, nil
}

// Uninstall removes a skill from the manifest.
// It does NOT remove the mod cache directory (that's a global cache).
func Uninstall(name string, scope SkillScope, workDir, homeDir string) error {
	manifestPath := ManifestPath(scope, workDir, homeDir)
	if manifestPath == "" {
		return fmt.Errorf("cannot determine manifest path for scope %q", scope)
	}

	mf, err := ReadManifest(manifestPath)
	if err != nil {
		return err
	}

	if _, ok := mf.Skills[name]; !ok {
		return fmt.Errorf("skill %q not found in %s manifest", name, scope)
	}

	delete(mf.Skills, name)
	return WriteManifest(manifestPath, mf)
}

// TidyResult records what Tidy did.
type TidyResult struct {
	Fixed  []string // skills that were re-downloaded
	Failed []string // skills that could not be restored
}

// Tidy checks all entries in the manifest and re-downloads any whose mod cache is missing.
func Tidy(ctx context.Context, scope SkillScope, workDir, homeDir, modDir string) (*TidyResult, error) {
	manifestPath := ManifestPath(scope, workDir, homeDir)
	if manifestPath == "" {
		return nil, fmt.Errorf("cannot determine manifest path for scope %q", scope)
	}

	mf, err := ReadManifest(manifestPath)
	if err != nil {
		return nil, err
	}

	result := &TidyResult{}
	updated := false

	for name, entry := range mf.Skills {
		dir, err := resolveModPath(modDir, entry.ModPath)
		if err != nil {
			result.Failed = append(result.Failed, name)
			continue
		}

		if _, err := os.Stat(filepath.Join(dir, skillFileName)); err == nil {
			continue // present, no action needed
		}

		// Missing — attempt re-download
		sk, restoreErr := tryRestore(ctx, entry, modDir)
		if restoreErr != nil {
			result.Failed = append(result.Failed, name)
			continue
		}

		newModPath, relErr := makeModPath(modDir, sk.BackingDir)
		if relErr != nil {
			result.Failed = append(result.Failed, name)
			continue
		}

		entry.ModPath = newModPath
		entry.Commit = resolveCommitFromModManifest(modDir, buildRefFromEntry(entry))
		updated = true
		result.Fixed = append(result.Fixed, name)
	}

	if updated {
		if err := WriteManifest(manifestPath, mf); err != nil {
			return result, err
		}
	}

	return result, nil
}

// deriveSubPath computes the per-skill subpath within a cloned repo.
// For single-skill installs (SubPath already set on ref), returns that.
// For fan-out installs, derives the relative path from BackingDir.
func deriveSubPath(sk *ResolvedSkill, ref *RemoteRef) string {
	if ref == nil || ref.Kind != RemoteGitHub {
		return ""
	}
	if ref.SubPath != "" {
		return ref.SubPath
	}
	// Fan-out: BackingDir is like <modDir>/<cacheKey>_<commit>/skills/<skill-name>
	// We want the path relative to the clone root (everything after the cacheKey dir).
	// SourceDir format: "<cloneDir>/<relative-path>"
	// The skill's source locator contains the clone URL + path info
	if sk.Source.SourceDir != "" {
		// Find the cache root by walking up from SourceDir to find the mod cache entry
		// The BackingDir is the skill dir itself; we need the relative path within the clone
		dir := sk.Source.SourceDir
		base := filepath.Base(dir)
		parent := filepath.Dir(dir)
		parentBase := filepath.Base(parent)
		// Common pattern: .../skills/<name> or just .../name
		if parentBase == "skills" {
			return "skills/" + base
		}
		return base
	}
	return ""
}

// buildRefFromEntry reconstructs a RemoteRef from a ManifestEntry for commit lookup.
func buildRefFromEntry(entry *ManifestEntry) *RemoteRef {
	if entry.Ref == "" || entry.Ref == "http" {
		return nil
	}
	_, ref, err := ClassifyUses(entry.Source)
	if err != nil {
		return nil
	}
	return ref
}

// refString extracts a human-readable ref from a RemoteRef.
func refString(ref *RemoteRef) string {
	if ref == nil {
		return ""
	}
	if ref.Kind == RemoteHTTP {
		return "http"
	}
	return ref.Ref
}

// resolveCommitFromModManifest reads the mod-level remote-skills.json to find the commit hash.
func resolveCommitFromModManifest(modDir string, ref *RemoteRef) string {
	if ref == nil || ref.Kind != RemoteGitHub {
		return ""
	}
	mf, err := readManifest(modDir)
	if err != nil {
		return ""
	}
	key := ref.CloneURL + "@" + ref.Ref
	if entry, ok := mf.Entries[key]; ok {
		return entry.Commit
	}
	return ""
}
