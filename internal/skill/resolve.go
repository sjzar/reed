package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sjzar/reed/internal/model"
)

// resolveUsesLocal resolves a local skill spec with a `uses` path relative to workflowDir.
func resolveUsesLocal(id string, uses string, workflowDir string) (*ResolvedSkill, error) {
	dir := filepath.Join(workflowDir, uses)
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("skill %q: resolve uses %q: %w", id, uses, err)
	}

	if info.IsDir() {
		// Directory: must contain SKILL.md
		sk, err := scanDir(dir)
		if err != nil {
			return nil, fmt.Errorf("skill %q: %w", id, err)
		}
		if err := ValidateNameMatchesID(id, sk.Meta); err != nil {
			return nil, fmt.Errorf("skill %q: %w", id, err)
		}
		sk.ID = id
		sk.Source = SkillSource{Kind: SourceWorkflowPath, Locator: uses, SourceDir: dir}
		return sk, nil
	}

	// Single file: must be named SKILL.md
	if filepath.Base(dir) != skillFileName {
		return nil, fmt.Errorf("skill %q: uses file must be named %s, got %q", id, skillFileName, filepath.Base(dir))
	}

	data, err := os.ReadFile(dir)
	if err != nil {
		return nil, fmt.Errorf("skill %q: read %s: %w", id, dir, err)
	}

	meta, body, err := ParseFrontMatter(data)
	if err != nil {
		return nil, fmt.Errorf("skill %q: parse %s: %w", id, dir, err)
	}

	if err := ValidateMeta(meta); err != nil {
		return nil, fmt.Errorf("skill %q: %w", id, err)
	}
	if err := ValidateNameMatchesID(id, meta); err != nil {
		return nil, fmt.Errorf("skill %q: %w", id, err)
	}

	files := []ResolvedFile{{Path: filepath.Base(dir), Content: data}}
	digest := ComputeDigest(files)

	return &ResolvedSkill{
		ID:         id,
		Meta:       meta,
		Body:       body,
		Files:      files,
		Source:     SkillSource{Kind: SourceWorkflowPath, Locator: uses, SourceDir: filepath.Dir(dir)},
		Backing:    BackingSource,
		BackingDir: filepath.Dir(dir),
		Digest:     digest,
	}, nil
}

// resolveResources resolves a skill spec with inline resources.
func resolveResources(id string, resources []model.SkillResourceSpec, _ /* workflowDir */, modDir string) (*ResolvedSkill, error) {
	var files []ResolvedFile
	var meta SkillMeta
	var body string
	skillMDCount := 0

	for _, r := range resources {
		relPath := r.Path
		content := []byte(r.Content)

		files = append(files, ResolvedFile{Path: relPath, Content: content})

		// Parse SKILL.md for meta if present
		if relPath == skillFileName {
			skillMDCount++
			var err error
			meta, body, err = ParseFrontMatter(content)
			if err != nil {
				return nil, fmt.Errorf("skill %q: parse SKILL.md: %w", id, err)
			}
		}
	}

	if skillMDCount != 1 {
		return nil, fmt.Errorf("skill %q: resources must include exactly one %s, found %d", id, skillFileName, skillMDCount)
	}

	if err := ValidateMeta(meta); err != nil {
		return nil, fmt.Errorf("skill %q: %w", id, err)
	}
	if err := ValidateNameMatchesID(id, meta); err != nil {
		return nil, fmt.Errorf("skill %q: %w", id, err)
	}

	digest := ComputeDigest(files)

	// Materialize to mod cache (content-addressed, idempotent)
	dirName := DigestDirName(id, digest)
	finalDir := filepath.Join(modDir, dirName)

	if _, err := os.Stat(finalDir); err != nil {
		// Not yet materialized — write atomically
		tmpDir, err := os.MkdirTemp(modDir, id+"_tmp_")
		if err != nil {
			return nil, fmt.Errorf("skill %q: create temp dir: %w", id, err)
		}
		for _, f := range files {
			dst := filepath.Join(tmpDir, f.Path)
			if dir := filepath.Dir(dst); dir != tmpDir {
				if err := os.MkdirAll(dir, 0755); err != nil {
					os.RemoveAll(tmpDir)
					return nil, fmt.Errorf("skill %q: create subdir for %s: %w", id, f.Path, err)
				}
			}
			if err := os.WriteFile(dst, f.Content, 0644); err != nil {
				os.RemoveAll(tmpDir)
				return nil, fmt.Errorf("skill %q: write %s: %w", id, f.Path, err)
			}
		}
		if err := os.Rename(tmpDir, finalDir); err != nil {
			os.RemoveAll(tmpDir)
			// Concurrent writer may have won — check if finalDir exists now
			if _, statErr := os.Stat(finalDir); statErr != nil {
				return nil, fmt.Errorf("skill %q: materialize to mod cache: %w", id, err)
			}
		}
	}

	return &ResolvedSkill{
		ID:         id,
		Meta:       meta,
		Body:       body,
		Files:      files,
		Source:     SkillSource{Kind: SourceWorkflowPath, Locator: id},
		Backing:    BackingMod,
		BackingDir: finalDir,
		Digest:     digest,
	}, nil
}

// LoadWorkflow resolves all workflow skill specs into ResolvedSkills.
// Remote skills with no SubPath produce 1:N fan-out with namespace-prefixed keys.
func LoadWorkflow(ctx context.Context, specs map[string]model.SkillSpec, workflowDir, modDir string) (map[string]*ResolvedSkill, error) {
	result := make(map[string]*ResolvedSkill, len(specs))
	for id, spec := range specs {
		switch {
		case spec.Uses != "":
			isRemote, ref, err := ClassifyUses(spec.Uses)
			if err != nil {
				return nil, fmt.Errorf("skill %q: %w", id, err)
			}
			if isRemote {
				skills, err := resolveRemote(ctx, id, ref, modDir)
				if err != nil {
					return nil, err
				}
				for k, sk := range skills {
					if existing, ok := result[k]; ok {
						return nil, fmt.Errorf("skill %q: key %q conflicts with already-resolved skill from %q", id, k, existing.Source.Locator)
					}
					result[k] = sk
				}
			} else {
				sk, err := resolveUsesLocal(id, spec.Uses, workflowDir)
				if err != nil {
					return nil, err
				}
				if existing, ok := result[id]; ok {
					return nil, fmt.Errorf("skill %q: conflicts with already-resolved skill from %q", id, existing.Source.Locator)
				}
				result[id] = sk
			}
		case len(spec.Resources) > 0:
			sk, err := resolveResources(id, spec.Resources, workflowDir, modDir)
			if err != nil {
				return nil, err
			}
			if existing, ok := result[id]; ok {
				return nil, fmt.Errorf("skill %q: conflicts with already-resolved skill from %q", id, existing.Source.Locator)
			}
			result[id] = sk
		default:
			return nil, fmt.Errorf("skill %q: must have uses or resources", id)
		}
	}
	return result, nil
}

// ValidateWorkflowSkills performs lightweight validation of workflow skill specs.
// Suitable for `reed validate` — reads and parses front matter for validation.
// For remote refs, validates URL format only (no network access).
func ValidateWorkflowSkills(specs map[string]model.SkillSpec, workflowDir string) error {
	for id, spec := range specs {
		if err := ValidateSkillSpec(id, spec); err != nil {
			return err
		}
		if spec.Uses != "" {
			// Check if this is a remote ref — validate format only, no network
			isRemote, ref, err := ClassifyUses(spec.Uses)
			if err != nil {
				return fmt.Errorf("skill %q: %w", id, err)
			}
			if isRemote {
				if err := ValidateRemoteRef(ref); err != nil {
					return fmt.Errorf("skill %q: %w", id, err)
				}
				continue
			}

			// Local path validation
			path := filepath.Join(workflowDir, spec.Uses)
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("skill %q: uses path %q not found: %w", id, spec.Uses, err)
			}
			if info.IsDir() {
				data, err := os.ReadFile(filepath.Join(path, skillFileName))
				if err != nil {
					return fmt.Errorf("skill %q: read %s: %w", id, skillFileName, err)
				}
				meta, _, err := ParseFrontMatter(data)
				if err != nil {
					return fmt.Errorf("skill %q: parse %s: %w", id, skillFileName, err)
				}
				if err := ValidateMeta(meta); err != nil {
					return fmt.Errorf("skill %q: %w", id, err)
				}
				if err := ValidateNameMatchesID(id, meta); err != nil {
					return fmt.Errorf("skill %q: %w", id, err)
				}
				if err := ValidateNameMatchesDir(filepath.Base(path), meta); err != nil {
					return fmt.Errorf("skill %q: %w", id, err)
				}
			} else {
				if filepath.Base(path) != skillFileName {
					return fmt.Errorf("skill %q: uses file must be named %s, got %q", id, skillFileName, filepath.Base(path))
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("skill %q: read %s: %w", id, path, err)
				}
				meta, _, err := ParseFrontMatter(data)
				if err != nil {
					return fmt.Errorf("skill %q: parse %s: %w", id, path, err)
				}
				if err := ValidateMeta(meta); err != nil {
					return fmt.Errorf("skill %q: %w", id, err)
				}
				if err := ValidateNameMatchesID(id, meta); err != nil {
					return fmt.Errorf("skill %q: %w", id, err)
				}
			}
		}
		if len(spec.Resources) > 0 {
			for _, r := range spec.Resources {
				if r.Path == skillFileName {
					meta, _, err := ParseFrontMatter([]byte(r.Content))
					if err != nil {
						return fmt.Errorf("skill %q: parse inline SKILL.md: %w", id, err)
					}
					if err := ValidateMeta(meta); err != nil {
						return fmt.Errorf("skill %q: %w", id, err)
					}
					if err := ValidateNameMatchesID(id, meta); err != nil {
						return fmt.Errorf("skill %q: %w", id, err)
					}
					break
				}
			}
		}
	}
	return nil
}
