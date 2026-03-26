package skill

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/sjzar/reed/internal/model"
)

// Service manages the skill lifecycle: scanning, resolving, and mounting.
type Service struct {
	homeDir string
	modDir  string

	mu      sync.RWMutex
	catalog map[string]*ResolvedSkill
}

// New creates a new skill Service.
func New(homeDir, modDir string) *Service {
	return &Service{
		homeDir: homeDir,
		modDir:  modDir,
		catalog: make(map[string]*ResolvedSkill),
	}
}

// ScanInstalled scans project, local, and global skill layers.
// Runtime callers pass autoRestore=true; diagnostic callers pass false.
func (s *Service) ScanInstalled(ctx context.Context, workDir string) error {
	result, err := ScanInstalled(ctx, workDir, s.homeDir, s.modDir, true)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sk := range result.Effective {
		s.catalog[id] = sk
	}
	return nil
}

// ScanInstalledDiag scans all layers in diagnostic mode (no auto-restore).
// Returns the full ScanResult including missing/shadowed entries.
func (s *Service) ScanInstalledDiag(ctx context.Context, workDir string) (*ScanResult, error) {
	return ScanInstalled(ctx, workDir, s.homeDir, s.modDir, false)
}

// LoadWorkflow resolves workflow skill specs and adds them to the catalog.
// Returns an error if a workflow skill conflicts with an installed skill,
// or if a plain ID and namespace prefix collide (e.g. "tool" and "tool.xxx").
func (s *Service) LoadWorkflow(ctx context.Context, specs map[string]model.SkillSpec, workflowDir string) error {
	resolved, err := LoadWorkflow(ctx, specs, workflowDir, s.modDir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sk := range resolved {
		if existing, ok := s.catalog[id]; ok && existing.Source.Kind == SourceInstalled {
			return fmt.Errorf("skill %q: conflict between workflow and installed skill", id)
		}

		// ID / namespace mutual exclusion:
		// A plain skill ID "tool" and a namespace "tool" (producing "tool.xxx")
		// are mutually exclusive to prevent LLM prefix-matching ambiguity.
		if strings.Contains(id, ".") {
			// Adding a namespaced key like "tool.xxx" — check if plain "tool" exists
			ns := id[:strings.Index(id, ".")]
			if _, ok := s.catalog[ns]; ok {
				return fmt.Errorf("skill %q: namespace prefix %q conflicts with existing skill ID %q", id, ns, ns)
			}
		} else {
			// Adding a plain key like "tool" — check if any "tool.*" exists
			prefix := id + "."
			for existingID := range s.catalog {
				if strings.HasPrefix(existingID, prefix) {
					return fmt.Errorf("skill %q: conflicts with existing namespaced skill %q", id, existingID)
				}
			}
		}

		s.catalog[id] = sk
	}
	return nil
}

// ListInstalled returns SkillInfo for all skills in the catalog (without mounting).
func (s *Service) ListInstalled() []SkillInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	infos := make([]SkillInfo, 0, len(s.catalog))
	for _, sk := range s.catalog {
		infos = append(infos, SkillInfo{
			ID:           sk.ID,
			Name:         sk.Meta.Name,
			Description:  sk.Meta.Description,
			AllowedTools: sk.Meta.AllowedTools,
		})
	}
	return infos
}

// ListAndMount mounts the requested skills and returns their info.
// This is the lazy mount entry point — skills are mounted on first access.
func (s *Service) ListAndMount(runRoot string, ids []string) ([]SkillInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var infos []SkillInfo
	for _, id := range ids {
		sk, ok := s.catalog[id]
		if !ok {
			return nil, fmt.Errorf("skill %q not found in catalog", id)
		}
		mountDir, err := EnsureMounted(runRoot, sk)
		if err != nil {
			return nil, err
		}
		infos = append(infos, SkillInfo{
			ID:           sk.ID,
			Name:         sk.Meta.Name,
			Description:  sk.Meta.Description,
			MountDir:     mountDir,
			BackingDir:   sk.BackingDir,
			AllowedTools: sk.Meta.AllowedTools,
		})
	}
	return infos, nil
}

// Get returns a resolved skill by ID.
func (s *Service) Get(id string) (*ResolvedSkill, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sk, ok := s.catalog[id]
	return sk, ok
}

// ResolveEnabled returns resolved skills for the given IDs.
func (s *Service) ResolveEnabled(ids []string) ([]*ResolvedSkill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*ResolvedSkill
	for _, id := range ids {
		sk, ok := s.catalog[id]
		if !ok {
			return nil, fmt.Errorf("skill %q not found in catalog", id)
		}
		result = append(result, sk)
	}
	return result, nil
}

// EnsureMounted mounts a single skill by ID and returns the mount directory.
func (s *Service) EnsureMounted(runRoot, id string) (string, error) {
	s.mu.RLock()
	sk, ok := s.catalog[id]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("skill %q not found in catalog", id)
	}
	return EnsureMounted(runRoot, sk)
}

// ResolveCLISkills resolves a mix of local skill IDs and remote URLs.
// Remote URLs are fetched, added to the catalog, and their catalog keys returned.
// Local IDs are returned as-is (they must already exist in the catalog from ScanInstalled).
func (s *Service) ResolveCLISkills(ctx context.Context, refs []string) ([]string, error) {
	specs := make(map[string]model.SkillSpec)
	var localIDs []string

	for _, ref := range refs {
		isRemote, _, err := ClassifyUses(ref)
		if err != nil {
			return nil, fmt.Errorf("invalid skill ref %q: %w", ref, err)
		}
		if isRemote {
			id := DeriveIDFromUses(ref)
			if prev, ok := specs[id]; ok {
				return nil, fmt.Errorf("skill ID collision: %q and %q both derive ID %q", prev.Uses, ref, id)
			}
			specs[id] = model.SkillSpec{Uses: ref}
		} else {
			localIDs = append(localIDs, ref)
		}
	}

	if len(specs) > 0 {
		// Use Service.LoadWorkflow to get conflict checks (installed skill,
		// namespace mutual exclusion) for free.
		if err := s.LoadWorkflow(ctx, specs, ""); err != nil {
			return nil, fmt.Errorf("resolve remote skills: %w", err)
		}

		// Collect resolved keys — fan-out may produce more keys than input specs.
		s.mu.RLock()
		for id := range s.catalog {
			for specID := range specs {
				if id == specID || strings.HasPrefix(id, specID+".") {
					localIDs = append(localIDs, id)
				}
			}
		}
		s.mu.RUnlock()
	}

	return localIDs, nil
}

// AllIDs returns all skill IDs in the catalog.
func (s *Service) AllIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.catalog))
	for id := range s.catalog {
		ids = append(ids, id)
	}
	return ids
}
