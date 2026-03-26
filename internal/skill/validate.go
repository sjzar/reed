package skill

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sjzar/reed/internal/model"
)

var validSkillNameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ValidateSkillName checks that a skill ID is a valid kebab-case identifier.
func ValidateSkillName(name string) error {
	if !validSkillNameRe.MatchString(name) {
		return fmt.Errorf("invalid skill name %q: must be kebab-case (e.g. code-review)", name)
	}
	return nil
}

// ValidateMeta checks that required SkillMeta fields are present.
func ValidateMeta(meta SkillMeta) error {
	if meta.Name == "" {
		return fmt.Errorf("skill meta: missing required field: name")
	}
	if !validSkillNameRe.MatchString(meta.Name) {
		return fmt.Errorf("skill meta: invalid name %q: must be kebab-case", meta.Name)
	}
	if meta.Description == "" {
		return fmt.Errorf("skill meta: missing required field: description")
	}
	return nil
}

// ValidateNameMatchesID checks that the meta name matches the skill ID.
func ValidateNameMatchesID(id string, meta SkillMeta) error {
	if meta.Name != "" && meta.Name != id {
		return fmt.Errorf("skill %q: meta name %q does not match ID", id, meta.Name)
	}
	return nil
}

// ValidateNameMatchesDir checks that the meta name matches the directory name.
func ValidateNameMatchesDir(dirName string, meta SkillMeta) error {
	if meta.Name != "" && meta.Name != dirName {
		return fmt.Errorf("skill dir %q: meta name %q does not match directory", dirName, meta.Name)
	}
	return nil
}

// ValidateResources checks that resource specs are valid.
// Enforces: both path and content required, no abs paths, no traversal,
// no duplicate paths, exactly one root-level SKILL.md, no nested SKILL.md.
func ValidateResources(resources []model.SkillResourceSpec) error {
	seen := make(map[string]bool, len(resources))
	skillMDCount := 0

	for i, r := range resources {
		if r.Path == "" || r.Content == "" {
			return fmt.Errorf("resources[%d]: both path and content are required", i)
		}
		if filepath.IsAbs(r.Path) {
			return fmt.Errorf("resources[%d]: path must be relative, got %q", i, r.Path)
		}
		cleaned := filepath.Clean(r.Path)
		if cleaned != r.Path || containsDotDotSegment(cleaned) {
			return fmt.Errorf("resources[%d]: path must not contain traversal, got %q", i, r.Path)
		}

		// Reject nested SKILL.md (e.g. "subdir/SKILL.md")
		if r.Path != skillFileName && filepath.Base(r.Path) == skillFileName {
			return fmt.Errorf("resources[%d]: %s must be at root, not nested: %q", i, skillFileName, r.Path)
		}

		if seen[r.Path] {
			return fmt.Errorf("resources[%d]: duplicate path %q", i, r.Path)
		}
		seen[r.Path] = true

		if r.Path == skillFileName {
			skillMDCount++
		}
	}
	if len(resources) > 0 && skillMDCount != 1 {
		return fmt.Errorf("resources must include exactly one %s, found %d", skillFileName, skillMDCount)
	}
	return nil
}

// containsDotDotSegment checks if any path segment is "..".
func containsDotDotSegment(p string) bool {
	for _, seg := range strings.Split(p, string(filepath.Separator)) {
		if seg == ".." {
			return true
		}
	}
	return false
}

// ValidateSkillSpec validates a single workflow skill spec.
func ValidateSkillSpec(id string, spec model.SkillSpec) error {
	if spec.Uses == "" && len(spec.Resources) == 0 {
		return fmt.Errorf("skill %q: must have uses or resources", id)
	}
	if spec.Uses != "" && len(spec.Resources) > 0 {
		return fmt.Errorf("skill %q: uses and resources are mutually exclusive", id)
	}
	if err := ValidateResources(spec.Resources); err != nil {
		return fmt.Errorf("skill %q: %w", id, err)
	}
	return nil
}

// ValidateRemoteRef validates a RemoteRef for basic correctness.
func ValidateRemoteRef(ref *RemoteRef) error {
	if ref == nil {
		return fmt.Errorf("remote ref is nil")
	}
	switch ref.Kind {
	case RemoteGitHub:
		if ref.CloneURL == "" {
			return fmt.Errorf("remote ref: missing clone URL")
		}
		if ref.Ref == "" {
			return fmt.Errorf("remote ref: missing git ref")
		}
	case RemoteHTTP:
		if ref.RawURL == "" {
			return fmt.Errorf("remote ref: missing raw URL")
		}
	default:
		return fmt.Errorf("remote ref: unknown kind %q", ref.Kind)
	}
	if containsDotDotSegment(ref.SubPath) {
		return fmt.Errorf("remote ref: path traversal not allowed in subpath %q", ref.SubPath)
	}
	return nil
}
