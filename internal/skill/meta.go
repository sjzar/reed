// Package skill provides skill lifecycle management: scanning, resolving, mounting, and serving.
package skill

// SourceKind identifies where a skill definition came from.
type SourceKind string

const (
	SourceInstalled    SourceKind = "installed"     // from manifest or project skills/
	SourceWorkflowPath SourceKind = "workflow_path" // from workflow skills.<id>.uses: ./path
	SourceRemote       SourceKind = "remote"        // from GitHub repo or HTTP URL
)

// SkillScope identifies which scope a skill was loaded from.
type SkillScope string

const (
	ScopeProject SkillScope = "project" // <workDir>/skills/
	ScopeLocal   SkillScope = "local"   // <workDir>/.reed/skills.json
	ScopeGlobal  SkillScope = "global"  // ~/.reed/skills.json
)

// SkillEntry is the full view of a skill across all scopes, for list/audit.
type SkillEntry struct {
	Name       string
	Scope      SkillScope
	Source     string // from manifest, e.g. "github.com/user/repo@main"
	Ref        string // git ref or "http"
	Commit     string // resolved commit hash
	IsShadowed bool   // overridden by a higher-priority scope
	IsMissing  bool   // mod_path target directory does not exist
}

// RemoteKind identifies the type of remote skill source.
type RemoteKind string

const (
	RemoteGitHub RemoteKind = "github"
	RemoteHTTP   RemoteKind = "http"
)

// RemoteRef describes a remote skill source location.
type RemoteRef struct {
	Kind     RemoteKind // github or http
	CloneURL string     // e.g. "https://github.com/user/repo.git"
	Ref      string     // git ref (tag/branch/commit), default "main"
	SubPath  string     // path within repo (empty = discover all)
	RawURL   string     // for HTTP single-file download
}

// BackingKind identifies the backing storage for a resolved skill.
type BackingKind string

const (
	BackingSource BackingKind = "source" // original source directory
	BackingMod    BackingKind = "mod"    // content-addressed mod cache
)

// SkillMeta holds the parsed front matter of a SKILL.md file.
type SkillMeta struct {
	Name          string         `yaml:"name"`
	Description   string         `yaml:"description"`
	License       string         `yaml:"license"`
	Compatibility string         `yaml:"compatibility"`
	AllowedTools  []string       `yaml:"allowed_tools"`
	Metadata      map[string]any `yaml:"metadata"`
}

// SkillSource records where a skill was loaded from.
type SkillSource struct {
	Kind      SourceKind
	Locator   string // e.g. "./skills/review" or "code-review"
	SourceDir string // absolute path to the source directory
}

// ResolvedFile is a single file within a resolved skill.
type ResolvedFile struct {
	Path    string // relative path within the skill directory
	Content []byte
}

// ResolvedSkill is a fully resolved skill ready for mounting.
type ResolvedSkill struct {
	ID         string
	Meta       SkillMeta
	Body       string         // SKILL.md body (after front matter)
	Files      []ResolvedFile // all files including SKILL.md
	Source     SkillSource
	Backing    BackingKind
	BackingDir string // absolute path to backing directory
	Digest     string // full SHA-256 hex of all files
	Scope      SkillScope
	IsShadowed bool
}

// SkillInfo is the public view of a mounted skill, returned to callers.
type SkillInfo struct {
	ID           string
	Name         string
	Description  string
	MountDir     string // absolute path to mounted skill directory
	BackingDir   string // absolute path to skill source directory (may differ from MountDir for symlinks)
	AllowedTools []string
}
