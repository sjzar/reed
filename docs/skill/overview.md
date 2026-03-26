---
title: Skill System
summary: Skill lifecycle — 3-tier scanning, remote resolution, manifest management, mounting, agent integration
read_when: implementing skills, understanding skill resolution, debugging skill loading, managing installed skills
---

# Skill System

Package: `internal/skill`

## Overview

Skills are self-contained capability bundles defined by a `SKILL.md` file with YAML frontmatter. The skill system manages scanning across 3 scope tiers, remote fetching (GitHub/HTTPS), manifest-based installation, content-addressed caching, mounting into run directories, and agent prompt integration. Note: skill body content is NOT directly injected into the agent prompt — the agent reads mounted `SKILL.md` files via tools at runtime.

## Core Types

```
SourceKind:  installed | workflow_path | remote
SkillScope:  project | local | global
BackingKind: source | mod
RemoteKind:  github | http

SkillMeta:      name, description, license, compatibility, allowed_tools, metadata
SkillSource:    Kind(SourceKind), Locator, SourceDir
ResolvedFile:   Path (relative), Content ([]byte)
ResolvedSkill:  ID, Meta, Body, Files, Source, Backing, BackingDir, Digest, Scope, IsShadowed
SkillInfo:      ID, Name, Description, MountDir, BackingDir, AllowedTools  (public view)
SkillEntry:     Name, Scope, Source, Ref, Commit, IsShadowed, IsMissing    (list/audit view)
RemoteRef:      Kind, CloneURL, Ref, SubPath, RawURL
ManifestEntry:  ModPath, Source, Ref, Commit, SubPath, InstalledAt
```

## Service API

`Service` holds a `catalog` (map[string]*ResolvedSkill) protected by `sync.RWMutex`. Constructor: `New(homeDir, modDir)`.

| Method | Purpose |
|---|---|
| `ScanInstalled(ctx, workDir)` | Scan 3 tiers with auto-restore for missing skills |
| `ScanInstalledDiag(ctx, workDir)` | Diagnostic scan — marks missing/shadowed without auto-restore |
| `LoadWorkflow(ctx, specs, workflowDir)` | Resolve workflow skill specs, add to catalog. Errors on conflicts |
| `ListInstalled()` | Return `[]SkillInfo` without mounting |
| `ListAndMount(runRoot, ids)` | Lazy mount on first access, return `[]SkillInfo` with MountDir |
| `Get(id)` | Catalog lookup by ID |
| `ResolveEnabled(ids)` | Batch lookup, returns `[]*ResolvedSkill` |
| `EnsureMounted(runRoot, id)` | Mount single skill by ID |
| `ResolveCLISkills(ctx, refs)` | Resolve mix of local IDs and remote URLs for CLI use. Remote refs are fetched and validated; local IDs are returned as-is (validated only at mount time) |
| `AllIDs()` | Return all catalog skill IDs |

## Lifecycle: Scan → Resolve → Validate → Mount → Inject

### Phase 1: Scan (3-tier)

`ScanInstalled` reads three layers with ascending priority (higher shadows lower):

1. **Global manifest** `~/.reed/skills.json` — lowest priority
2. **Local manifest** `<workDir>/.reed/skills.json` — medium priority
3. **Project directory** `<workDir>/skills/<id>/` — highest priority, directory scan

Shadowing: when the same skill ID appears in multiple scopes, the higher-priority scope wins. Lower-priority entries are marked `IsShadowed=true`.

Auto-restore (`autoRestore=true`, runtime mode): if `scanDirRaw` fails for any reason (missing directory, corrupt YAML, symlink violation, etc.), attempts re-download from recorded source. Diagnostic mode (`autoRestore=false`): marks as `IsMissing` for display on any scan failure.

Scanning variants: project skills use `scanDir` (validates meta name matches directory name). Manifest-installed and remote skills use `scanDirRaw` (skips directory-name validation, since mod cache directory names may differ from skill names).

### Phase 2: Resolve (Workflow Skills)

`LoadWorkflow` resolves workflow `SkillSpec` entries. Three modes:

**`uses` local path**: resolves relative to `workflowDir`. `ClassifyUses` treats any non-remote string as local (including `./`, `../`, bare paths). Can point to a directory (must contain `SKILL.md`, meta name must match skill ID and directory name) or a single `SKILL.md` file (meta name must match skill ID only, no directory-name check).

**`uses` remote**: GitHub or HTTPS URL → classifies via `ClassifyUses`, downloads via `resolveRemote`. See [Remote Skills](#remote-skills).

**`resources` inline**: List of `{path, content}` pairs. Must include exactly one root-level `SKILL.md`. Materialized to content-addressed mod cache directory (`<modDir>/<id>_<digest[:12]>/`). Atomic write via temp dir + rename.

`uses` and `resources` are mutually exclusive per spec.

### Phase 3: Validate

| Validator | Rule |
|---|---|
| `ValidateSkillName` | Kebab-case: `^[a-z][a-z0-9]*(-[a-z0-9]+)*$` |
| `ValidateMeta` | `name` (required, kebab-case), `description` (required) |
| `ValidateNameMatchesID` | Meta name must equal skill ID |
| `ValidateNameMatchesDir` | Meta name must equal directory name |
| `ValidateSkillSpec` | `uses` XOR `resources` (mutually exclusive, at least one required); delegates to `ValidateResources` |
| `ValidateResources` | Both path+content required, no absolute paths, no `..` traversal, no duplicates, exactly one root `SKILL.md` |
| `ValidateRemoteRef` | Basic correctness of RemoteRef fields |
| `ValidateWorkflowSkills` | Lightweight validation for `reed validate` — no network for remotes |

Note: `LoadWorkflow` does not call `ValidateSkillSpec` — it performs equivalent checks inline during resolution. Full validation via `ValidateSkillSpec` is invoked by the workflow validator (`ValidateWorkflowSkills`).

### Phase 4: Mount

`EnsureMounted(runRoot, sk)` mounts at `<runRoot>/skills/<id>/`. Strategy:

1. If already mounted (directory exists) → return immediately (idempotent)
2. Try symlink to `BackingDir` (fast path)
3. Fall back to recursive file copy

### Phase 5: Agent Integration

1. `engine.resolve()` → `resolveToolDefs()` → `resolveSkillInfos()`
2. `skillSvc.ListAndMount(runRoot, reqSkills)` mounts skills
3. Security Guard grants read access to skill MountDir and BackingDir
4. `buildSkillSummary()` injects a **summary** into system prompt (PriorityLow, MinMode=PromptModeFull):
   - Skill ID, description, mount path for each skill
   - Entry point: `SKILL.md` (agent reads content via tools at runtime)
   - Shell env access: `$REED_RUN_SKILL_DIR/<skill-id>/`
   - When no skills are mounted, prompt says "No skills are loaded for this run."
   - Section is suppressed entirely in `prompt_mode=minimal` and `prompt_mode=none`
5. Skills are **advisory** — mount failures are logged as warnings, run continues

Tool dependency: when `with.tools` is NOT specified in the step, `AgentWorker` extracts `Meta.AllowedTools` from each skill in the workflow `AgentSpec.Skills` and adds them to the agent's tool set (core + skill tools, additive). When `with.tools` IS specified, skill-derived tools are skipped — the explicit tool list takes full control. Tool name normalization handles Claude-sanitized names (e.g., `mcp__srv__tool` → `mcp/srv/tool`).

Skill selection cascade (first non-empty wins, no merge): `AgentRunRequest.Skills` → `AgentSpec.Skills` (workflow) → `profile.SkillIDs` → none.

## Manifest System

Two JSON manifest files track installed skills:

| Scope | Path |
|---|---|
| Local | `<workDir>/.reed/skills.json` |
| Global | `~/.reed/skills.json` |

Format:
```json
{
  "skills": {
    "code-review": {
      "mod_path": "github.com_user_repo_abc123def456/skills/code-review",
      "source": "github.com/user/repo",
      "ref": "main",
      "commit": "abc123...",
      "sub_path": "skills/code-review",
      "installed_at": "2025-01-01T00:00:00Z"
    }
  }
}
```

`mod_path` is relative to modDir (forward slashes). `sub_path` records the skill's location within a cloned repo (for fan-out repos, enables targeted restore).

Manifest I/O: `ReadManifest` returns empty on missing file, errors on corrupt. `WriteManifest` uses atomic temp→rename. `resolveModPath` validates against traversal attacks and symlink escapes.

## Remote Skills

### Source Formats

**GitHub shorthand**:
- `github.com/user/repo` — discover all skills in repo (1:N fan-out)
- `github.com/user/repo@v1.0` — pinned ref
- `github.com/user/repo/path/to/skill` — specific skill (1:1)
- `github/user/repo` — shorthand without `.com`

**GitHub URL**:
- `https://github.com/user/repo/blob/main/path/SKILL.md`
- `https://github.com/user/repo/tree/main/path/to/skill`

**HTTPS single-file**:
- `https://example.com/skills/my-skill/SKILL.md` — must end with `/SKILL.md`
- Plaintext `http://` rejected for security

### Classification (`ClassifyUses`)

1. `./` or `../` prefix → local
2. Contains `github.com` → GitHub (shorthand or blob/tree URL)
3. `github/` prefix → GitHub shorthand
4. `https://` ending with `/SKILL.md` → HTTP
5. `http://` → rejected
6. Everything else → local

### Resolution

**GitHub with SubPath** (1:1): clone repo → navigate to subpath → scan skill directory. Symlink escape detection via `EvalSymlinks`.

**GitHub without SubPath** (1:N fan-out): clone repo → `discoverSkills()` → catalog keys are `<sourceID>.<skillName>`. Discovery priority: check `<repo>/skills/` first, fall back to full tree walk (max depth 5, skip `.git`, `node_modules`, `vendor`, `.github`, `testdata`).

**HTTP** (1:1): download single `SKILL.md` (30s timeout, 10MB max). Content-addressed cache.

### Git Caching

Clone via: `git init → remote add → fetch --depth 1 → checkout`. Tracked in `<modDir>/remote-skills.json`:
```json
{
  "entries": {
    "https://github.com/user/repo.git@main": {
      "clone_url": "...", "ref": "main", "commit": "abc...",
      "dir": "github.com_user_repo_abc123def456", "fetched_at": "..."
    }
  }
}
```

Cache key: `<domain>_<user>_<repo>` (slashes → underscores). Directory: `<cacheKey>_<commit[:12]>`.

Freshness: `resolveRemoteHash` uses `git ls-remote` to check remote commit. Pinned 40-char hex commits skip network. Ref fallback: `main` → `master`. Network failure → fall back to cached version if available.

Timeouts: git=120s, http=30s.

### ID Derivation (`DeriveIDFromUses`)

Extracts last path segment before `@version`, strips `/SKILL.md`. Validates as kebab-case. Falls back to `remote-<hash>` if derivation fails.

## Conflict Rules

`Service.LoadWorkflow` enforces:

1. **Installed vs. workflow**: workflow skill ID must not collide with an already-installed skill ID
2. **Workflow vs. workflow**: two workflow specs must not resolve to the same catalog key
3. **ID / namespace mutual exclusion**: a plain ID `tool` and a namespaced key `tool.xxx` cannot coexist — prevents LLM prefix-matching ambiguity

## Frontmatter Format

```yaml
---
name: my-skill
description: What this skill does
license: MIT
compatibility: "4.0"
allowed_tools:
  - bash
  - read
  - write
metadata:
  custom_field: value
---
Body content here (agent reads this via tools at runtime — NOT injected into prompt).
```

Parsed by `ParseFrontMatter`. Separator: `---`. Missing frontmatter → `ParseFrontMatter` treats entire content as body, but in practice all loading paths call `ValidateMeta` which requires `name` and `description`, so a frontmatter-less skill will fail validation.

Required: `name` (kebab-case), `description`. Optional: `license`, `compatibility`, `allowed_tools`, `metadata`.

## Digest

`ComputeDigest(files)` → SHA-256 hex. Files sorted by path. Each file contributes `path + NUL + content + NUL`.

`DigestDirName(id, digest)` → `"<id>_<digest[:12]>"` for mod cache directory naming.

## Security

- **Symlinks**: rejected during directory scans (`scanDir`/`scanDirRaw`) for both `SKILL.md` and all files under skill directories. Note: single-file `uses` resolution (`resolveUsesLocal` for a standalone `SKILL.md`) does not perform symlink checks
- **Path traversal**: `..` segments rejected in mod_path, resources, and remote subpaths
- **Symlink escape**: `EvalSymlinks` + prefix check on mod_path resolution and remote subpath navigation
- **HTTPS only**: plaintext HTTP rejected for remote skill URLs
- **Atomic writes**: temp→rename for manifests and mod cache materialization
- **Guard access**: read-only grants to MountDir and BackingDir at runtime

## CLI Commands

### `reed skill install <source> [-g]`

Downloads remote skill, registers in manifest. `-g` for global scope.

```
reed skill install github.com/user/my-skills
reed skill install github.com/user/my-skills@v1.0 -g
reed skill install github.com/user/repo/path/to/skill
```

Local paths rejected — copy directly to `./skills/`.

### `reed skill uninstall <name> [-g]`

Removes skill from manifest. Does NOT delete mod cache (global cache).

### `reed skill list`

Lists all skills across all scopes. Shows NAME, REF, SCOPE, SOURCE. Annotates `(SHADOWED)` and `(MISSING)`. Uses diagnostic scan (no auto-restore).

### `reed skill tidy [-g]`

Re-downloads missing skills from manifest sources. Updates manifest with new mod_path and commit. Reports fixed and failed skills.

## Storage Layout

```
~/.reed/
  skills.json              # global manifest
  mod/skills/              # mod cache (shared)
    remote-skills.json     # remote clone tracking
    github.com_user_repo_abc123def456/  # cloned repo
      skills/
        code-review/
          SKILL.md
    my-skill_def789abc012/ # content-addressed cache
      SKILL.md

<workDir>/
  .reed/
    skills.json            # local manifest
  skills/                  # project skills (highest priority)
    my-skill/
      SKILL.md

<runRoot>/
  skills/                  # mounted skills (runtime)
    my-skill/  →  symlink or copy
```

## Workflow DSL

```yaml
skills:
  code-review:
    uses: ./skills/code-review       # local directory
  my-helper:
    uses: ./path/to/SKILL.md         # single file
  remote-skill:
    uses: github.com/user/repo/skill # remote GitHub
  inline-skill:
    resources:                        # inline definition
      - path: SKILL.md
        content: |
          ---
          name: inline-skill
          description: Inline skill example
          ---
          Skill body here.

agents:
  reviewer:
    model: anthropic/claude-sonnet-4-20250514
    skills: [code-review, remote-skill]  # skills attached to agent, not step

jobs:
  default:
    steps:
      - uses: reviewer                  # step references agent by ID
```

## File Map

| File | Responsibility |
|---|---|
| `meta.go` | Type definitions (SourceKind, SkillScope, BackingKind, RemoteRef, SkillMeta, ResolvedSkill, SkillInfo, etc.) |
| `service.go` | Thread-safe Service with catalog management |
| `scan.go` | 3-tier scanning (global → local → project), shadowing, auto-restore |
| `resolve.go` | Workflow spec resolution (local uses, inline resources), LoadWorkflow |
| `remote.go` | Remote classification, GitHub/HTTP fetching, git caching, discovery, ID derivation |
| `install.go` | Install, Uninstall, Tidy operations |
| `manifest.go` | ManifestFile/ManifestEntry I/O, mod_path security validation |
| `validate.go` | Name, meta, resource, spec, remote ref validation |
| `mount.go` | EnsureMounted — symlink or copy to runRoot |
| `frontmatter.go` | YAML frontmatter parsing from SKILL.md |
| `digest.go` | SHA-256 digest computation, DigestDirName |
