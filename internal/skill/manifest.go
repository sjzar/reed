package skill

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ManifestFile is the on-disk JSON format for installed skill manifests.
// Stored at <workDir>/.reed/skills.json (local) or ~/.reed/skills.json (global).
type ManifestFile struct {
	Skills map[string]*ManifestEntry `json:"skills"`
}

// ManifestEntry records a single installed skill in the manifest.
type ManifestEntry struct {
	ModPath     string    `json:"mod_path"`           // relative to modDir, forward slashes
	Source      string    `json:"source"`             // e.g. "github.com/user/repo@main"
	Ref         string    `json:"ref"`                // git ref or "http"
	Commit      string    `json:"commit"`             // resolved commit hash
	SubPath     string    `json:"sub_path,omitempty"` // path within cloned repo (for fan-out repos)
	InstalledAt time.Time `json:"installed_at"`
}

// ReadManifest reads a skill manifest from the given path.
// Returns an empty manifest if the file does not exist; returns an error if the file is corrupt.
func ReadManifest(path string) (*ManifestFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ManifestFile{Skills: make(map[string]*ManifestEntry)}, nil
		}
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var mf ManifestFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if mf.Skills == nil {
		mf.Skills = make(map[string]*ManifestEntry)
	}
	// Strip nil entries (e.g. {"skills":{"foo":null}})
	for k, v := range mf.Skills {
		if v == nil {
			delete(mf.Skills, k)
		}
	}
	return &mf, nil
}

// WriteManifest atomically writes a skill manifest to the given path.
func WriteManifest(path string, mf *ManifestFile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".skills.json.tmp.*")
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

// resolveModPath resolves a manifest mod_path to an absolute directory,
// validating against path traversal attacks (including symlinks).
func resolveModPath(modDir, modPath string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(modPath))
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("mod_path must be relative: %q", modPath)
	}
	if containsDotDotSegment(cleaned) {
		return "", fmt.Errorf("mod_path contains path traversal: %q", modPath)
	}
	finalDir := filepath.Join(modDir, cleaned)

	// Resolve symlinks on modDir to get a canonical base for comparison.
	realMod, err := filepath.EvalSymlinks(modDir)
	if err != nil {
		realMod, _ = filepath.Abs(modDir)
	}

	// For the target path: if it exists, resolve symlinks to catch symlink escapes.
	// If it doesn't exist (MISSING case), build the canonical path from the
	// resolved modDir + the clean relative component (safe since we already
	// rejected .. segments).
	realFinal, err := filepath.EvalSymlinks(finalDir)
	if err != nil {
		// Target doesn't exist — construct from resolved modDir
		realFinal = filepath.Join(realMod, cleaned)
	}
	if !strings.HasPrefix(realFinal, realMod+string(filepath.Separator)) && realFinal != realMod {
		return "", fmt.Errorf("mod_path escapes mod directory: %q", modPath)
	}
	return finalDir, nil
}

// ManifestPath returns the skill manifest path for a given scope.
func ManifestPath(scope SkillScope, workDir, homeDir string) string {
	switch scope {
	case ScopeLocal:
		return filepath.Join(workDir, ".reed", "skills.json")
	case ScopeGlobal:
		return filepath.Join(homeDir, "skills.json")
	default:
		return ""
	}
}
