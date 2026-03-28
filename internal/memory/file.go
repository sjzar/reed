package memory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/sjzar/reed/internal/model"
)

// FileProvider implements Provider backed by MEMORY.md files on disk.
// Each scope key (namespace/agentID/sessionKey) maps to a directory
// containing a single MEMORY.md file.
type FileProvider struct {
	memoryDir string
	extractor LLMExtractor
	locks     sync.Map // scopeKey → *sync.Mutex
}

// NewFileProvider creates a FileProvider.
// memoryDir is the base directory (e.g. conf.MemoryDir()).
// extractor handles LLM-based fact extraction.
func NewFileProvider(memoryDir string, extractor LLMExtractor) *FileProvider {
	return &FileProvider{
		memoryDir: memoryDir,
		extractor: extractor,
	}
}

// BeforeRun reads the MEMORY.md for the given scope.
// Returns empty MemoryResult if the file doesn't exist or is unreadable.
func (p *FileProvider) BeforeRun(_ context.Context, rc RunContext) (MemoryResult, error) {
	scopeKey, err := buildScopeKey(rc)
	if err != nil {
		log.Warn().Err(err).Msg("invalid memory scope key, returning empty memory")
		return MemoryResult{}, nil
	}

	path := filepath.Join(p.memoryDir, scopeKey, memoryFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return MemoryResult{}, nil
		}
		log.Warn().Err(err).Str("path", path).Msg("failed to read MEMORY.md, returning empty memory")
		return MemoryResult{}, nil
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return MemoryResult{}, nil
	}
	return MemoryResult{Content: content}, nil
}

// AfterRun extracts memorable facts and persists them to MEMORY.md.
// Only triggers when user message count >= threshold and new assistant messages exist.
func (p *FileProvider) AfterRun(ctx context.Context, rc RunContext, messages []model.Message) error {
	if !shouldExtract(messages) {
		return nil
	}

	scopeKey, err := buildScopeKey(rc)
	if err != nil {
		return fmt.Errorf("invalid scope key: %w", err)
	}

	return p.withScopeLock(scopeKey, func() error {
		return p.extractAndPersist(ctx, scopeKey, messages)
	})
}

// extractAndPersist does the actual LLM extraction and file write.
// Must be called under scope lock.
func (p *FileProvider) extractAndPersist(ctx context.Context, scopeKey string, messages []model.Message) error {
	scopeDir := filepath.Join(p.memoryDir, scopeKey)
	memPath := filepath.Join(scopeDir, memoryFileName)

	// Read existing memory.
	existing := ""
	if data, err := os.ReadFile(memPath); err == nil {
		existing = strings.TrimSpace(string(data))
	} else if !errors.Is(err, fs.ErrNotExist) {
		log.Warn().Err(err).Str("path", memPath).Msg("failed to read existing memory, extracting without context")
	}

	// Extract facts via LLM.
	extracted, err := p.extractor.ExtractFacts(ctx, messages, existing)
	if err != nil {
		return fmt.Errorf("extract facts: %w", err)
	}

	// Merge.
	merged := mergeMemory(existing, extracted)
	if merged == existing {
		return nil // nothing new
	}

	// Consolidate if over size cap.
	if len(merged) > defaultMaxBytes {
		consolidated, err := p.extractor.ExtractFacts(ctx, nil, merged)
		if err != nil {
			log.Warn().Err(err).Msg("memory consolidation failed, keeping uncompressed")
		} else if consolidated != "" && !strings.EqualFold(consolidated, "NONE") {
			merged = strings.TrimSpace(consolidated)
		} else {
			log.Warn().Str("scope", scopeKey).Int("bytes", len(merged)).
				Msg("memory over budget, consolidation returned NONE — writing uncompressed")
		}
	}

	// Atomic write. MkdirAll is idempotent; withScopeLock already ensures the
	// dir exists for the lock file, but the first write may race before any lock.
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		return fmt.Errorf("create memory dir: %w", err)
	}

	// tmpPath is scope-locked so no concurrent writes to the same .tmp file.
	tmpPath := memPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(merged+"\n"), 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, memPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}

	log.Debug().Str("scope", scopeKey).Int("bytes", len(merged)).Msg("memory persisted")
	return nil
}

// withScopeLock acquires both in-process and cross-process locks for the scope.
//
// Two layers are needed because flock on Linux is per-open-file-description:
// two goroutines opening the same lock file separately each get their own
// description and flock does NOT serialize them. The in-process mutex handles
// same-process concurrency; flock handles cross-process (multiple CLI invocations).
func (p *FileProvider) withScopeLock(scopeKey string, fn func() error) error {
	// Layer 1: in-process mutex.
	mu := p.scopeMu(scopeKey)
	mu.Lock()
	defer mu.Unlock()

	// Layer 2: cross-process flock.
	scopeDir := filepath.Join(p.memoryDir, scopeKey)
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		return fmt.Errorf("create scope dir for lock: %w", err)
	}
	lockPath := filepath.Join(scopeDir, lockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer f.Close()

	if err := flockFile(f); err != nil {
		return fmt.Errorf("acquire flock: %w", err)
	}
	defer funlockFile(f) //nolint:errcheck

	return fn()
}

// scopeMu returns the in-process mutex for a scope key.
func (p *FileProvider) scopeMu(key string) *sync.Mutex {
	v, _ := p.locks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// buildScopeKey constructs the filesystem path components from RunContext.
func buildScopeKey(rc RunContext) (string, error) {
	ns, err := sanitizePathComponent(rc.Namespace)
	if err != nil {
		return "", fmt.Errorf("namespace: %w", err)
	}
	ag, err := sanitizePathComponent(rc.AgentID)
	if err != nil {
		return "", fmt.Errorf("agentID: %w", err)
	}
	sk, err := sanitizePathComponent(rc.SessionKey)
	if err != nil {
		return "", fmt.Errorf("sessionKey: %w", err)
	}
	return filepath.Join(ns, ag, sk), nil
}

// sanitizePathComponent validates a string for safe use as a filesystem path component.
func sanitizePathComponent(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("empty path component")
	}
	if s == ".." || s == "." {
		return "", fmt.Errorf("invalid path component: %q", s)
	}
	// filepath.Clean collapses ".." and redundant separators; if the result
	// differs from s, the input contained traversal or suspicious sequences.
	if filepath.Clean(s) != s || strings.ContainsAny(s, `/\:`) {
		return "", fmt.Errorf("invalid path component: %q", s)
	}
	return s, nil
}
