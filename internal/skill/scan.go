package skill

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const skillFileName = "SKILL.md"

// scanDir reads a single skill directory and returns a ResolvedSkill.
// The directory must contain a SKILL.md file with valid front matter.
// It validates that the meta name matches the directory name.
func scanDir(dir string) (*ResolvedSkill, error) {
	sk, err := scanDirRaw(dir)
	if err != nil {
		return nil, err
	}
	dirName := filepath.Base(dir)
	if err := ValidateNameMatchesDir(dirName, sk.Meta); err != nil {
		return nil, err
	}
	return sk, nil
}

// scanDirRaw reads a single skill directory and returns a ResolvedSkill
// without checking that the meta name matches the directory name.
// Use this for remote skills where directory names may differ from skill names.
func scanDirRaw(dir string) (*ResolvedSkill, error) {
	skillPath := filepath.Join(dir, skillFileName)

	// Lstat before read: reject symlinks for SKILL.md
	fi, err := os.Lstat(skillPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", skillPath, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("symlinks not allowed for %s: %s", skillFileName, skillPath)
	}

	data, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", skillPath, err)
	}

	meta, body, err := ParseFrontMatter(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", skillPath, err)
	}

	if err := ValidateMeta(meta); err != nil {
		return nil, fmt.Errorf("validate %s: %w", dir, err)
	}

	// Collect all files recursively
	var files []ResolvedFile
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks not allowed in skill directory: %s", path)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("compute relative path for %s: %w", path, err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read file %s: %w", rel, err)
		}
		files = append(files, ResolvedFile{Path: rel, Content: content})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk dir %s: %w", dir, err)
	}

	digest := ComputeDigest(files)

	return &ResolvedSkill{
		ID:         meta.Name,
		Meta:       meta,
		Body:       body,
		Files:      files,
		Backing:    BackingSource,
		BackingDir: dir,
		Digest:     digest,
	}, nil
}

// ScanResult contains the results of a 3-tier skill scan.
type ScanResult struct {
	Effective map[string]*ResolvedSkill // active skills for Worker execution
	Entries   []SkillEntry              // all entries including shadowed/missing, for list
}

// ScanInstalled scans for installed skills across three layers:
//  1. Global: ~/.reed/skills.json (lowest priority)
//  2. Local: <workDir>/.reed/skills.json
//  3. Project: <workDir>/skills/ (highest priority, directory scan)
//
// autoRestore controls behavior for missing mod_path entries:
//   - true (runtime): attempt to re-download from source
//   - false (diagnostic): mark as IsMissing for display
func ScanInstalled(ctx context.Context, workDir, homeDir, modDir string, autoRestore bool) (*ScanResult, error) {
	result := &ScanResult{Effective: make(map[string]*ResolvedSkill)}

	// 1. Global manifest (~/.reed/skills.json)
	globalPath := ManifestPath(ScopeGlobal, workDir, homeDir)
	if globalPath != "" {
		if err := scanManifestLayer(ctx, result, globalPath, ScopeGlobal, modDir, autoRestore); err != nil {
			return nil, fmt.Errorf("scan global skills: %w", err)
		}
	}

	// 2. Local manifest (<workDir>/.reed/skills.json)
	if workDir != "" {
		localPath := ManifestPath(ScopeLocal, workDir, homeDir)
		if err := scanManifestLayer(ctx, result, localPath, ScopeLocal, modDir, autoRestore); err != nil {
			return nil, fmt.Errorf("scan local skills: %w", err)
		}
	}

	// 3. Project directory (<workDir>/skills/) — highest priority
	if workDir != "" {
		projectDir := filepath.Join(workDir, "skills")
		if err := scanProjectDir(result, projectDir); err != nil {
			return nil, fmt.Errorf("scan project skills: %w", err)
		}
	}

	return result, nil
}

// scanManifestLayer reads a manifest file and adds its entries to the scan result.
// Skills from higher-priority layers shadow those from lower-priority ones.
func scanManifestLayer(ctx context.Context, result *ScanResult, manifestPath string, scope SkillScope, modDir string, autoRestore bool) error {
	mf, err := ReadManifest(manifestPath)
	if err != nil {
		return err
	}

	for name, entry := range mf.Skills {
		dir, err := resolveModPath(modDir, entry.ModPath)
		if err != nil {
			return err // security error: never skip
		}

		sk, scanErr := scanDirRaw(dir)

		if scanErr != nil {
			if autoRestore {
				sk, scanErr = tryRestore(ctx, entry, modDir)
				if scanErr != nil {
					return fmt.Errorf("skill %q: auto-restore failed: %w", name, scanErr)
				}
				// Validate restored skill matches expected name
				if sk.Meta.Name != name {
					return fmt.Errorf("skill %q: restored skill has name %q", name, sk.Meta.Name)
				}
				// Update manifest with new mod_path and commit after restore
				newModPath, relErr := makeModPath(modDir, sk.BackingDir)
				if relErr == nil {
					entry.ModPath = newModPath
					entry.Commit = resolveCommitFromModManifest(modDir, buildRefFromEntry(entry))
					_ = WriteManifest(manifestPath, mf)
				}
			} else {
				// Diagnostic mode: record MISSING
				result.Entries = append(result.Entries, SkillEntry{
					Name:      name,
					Scope:     scope,
					Source:    entry.Source,
					Ref:       entry.Ref,
					Commit:    entry.Commit,
					IsMissing: true,
				})
				continue
			}
		}

		sk.ID = name
		sk.Scope = scope
		sk.Source = SkillSource{Kind: SourceInstalled, Locator: name, SourceDir: dir}

		entryView := SkillEntry{
			Name:   name,
			Scope:  scope,
			Source: entry.Source,
			Ref:    entry.Ref,
			Commit: entry.Commit,
		}

		// Shadow check: if already in effective map, mark existing or this one
		if existing, ok := result.Effective[name]; ok {
			// This entry is lower priority (global shadowed by local)
			// or same — just mark it shadowed since we process low→high
			// Actually we process global first then local, so local should shadow global.
			// Mark the existing (lower priority) entry as shadowed
			existing.IsShadowed = true
			markShadowed(result, name, existing.Scope)
		}

		result.Effective[name] = sk
		result.Entries = append(result.Entries, entryView)
	}
	return nil
}

// scanProjectDir scans <workDir>/skills/ for skill subdirectories (highest priority).
func scanProjectDir(result *ScanResult, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subDir := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(subDir, skillFileName)); err != nil {
			continue
		}
		sk, err := scanDir(subDir)
		if err != nil {
			return fmt.Errorf("skill %s: %w", e.Name(), err)
		}
		sk.Source = SkillSource{Kind: SourceInstalled, Locator: sk.ID, SourceDir: subDir}
		sk.Scope = ScopeProject

		// Shadow any existing entry
		if existing, ok := result.Effective[sk.ID]; ok {
			existing.IsShadowed = true
			markShadowed(result, sk.ID, existing.Scope)
		}

		result.Effective[sk.ID] = sk
		result.Entries = append(result.Entries, SkillEntry{
			Name:  sk.ID,
			Scope: ScopeProject,
		})
	}
	return nil
}

// markShadowed marks all entries with the given name and scope as shadowed.
func markShadowed(result *ScanResult, name string, scope SkillScope) {
	for i := range result.Entries {
		if result.Entries[i].Name == name && result.Entries[i].Scope == scope {
			result.Entries[i].IsShadowed = true
		}
	}
}

// tryRestore attempts to re-download a missing skill from its manifest source.
// Uses entry.SubPath when available to resolve the exact skill from fan-out repos.
func tryRestore(ctx context.Context, entry *ManifestEntry, modDir string) (*ResolvedSkill, error) {
	isRemote, ref, err := ClassifyUses(entry.Source)
	if err != nil {
		return nil, fmt.Errorf("classify source %q: %w", entry.Source, err)
	}
	if !isRemote {
		return nil, fmt.Errorf("source %q is not a remote reference", entry.Source)
	}

	// If we have a SubPath, set it on the ref so resolveRemote targets the exact skill.
	if entry.SubPath != "" && ref.Kind == RemoteGitHub && ref.SubPath == "" {
		ref.SubPath = entry.SubPath
	}

	resolved, err := resolveRemote(ctx, "restore", ref, modDir)
	if err != nil {
		return nil, err
	}

	// Return the first resolved skill
	for _, sk := range resolved {
		return sk, nil
	}
	return nil, fmt.Errorf("no skills resolved from %q", entry.Source)
}

// makeModPath computes the mod_path (forward-slash relative path) from modDir to dir.
func makeModPath(modDir, dir string) (string, error) {
	absModDir, err := filepath.Abs(modDir)
	if err != nil {
		return "", err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absModDir, absDir)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}
