package security

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SecretSourceTypeFile is the source type for .env-formatted files.
const SecretSourceTypeFile = "file"

// SecretSource defines a single secrets data source.
type SecretSource struct {
	Type string // SecretSourceTypeFile
	Path string // absolute or ~/ prefixed path
}

// SecretStore is a read-only, thread-safe key-value store for secrets.
// It is loaded once at startup from configured sources.
type SecretStore struct {
	mu      sync.RWMutex
	secrets map[string]string
}

// NewSecretStore loads secrets from the given sources in order (last-wins).
// Returns a valid empty store if sources is nil or empty.
func NewSecretStore(sources []SecretSource) (*SecretStore, error) {
	merged := make(map[string]string)
	for _, src := range sources {
		switch src.Type {
		case SecretSourceTypeFile:
			m, err := loadSecretFile(src.Path)
			if err != nil {
				return nil, fmt.Errorf("secret source %q: %w", src.Path, err)
			}
			maps.Copy(merged, m)
		default:
			return nil, fmt.Errorf("unsupported secret source type: %q", src.Type)
		}
	}
	return &SecretStore{secrets: merged}, nil
}

// Get returns the value and existence of a secret by name.
func (s *SecretStore) Get(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.secrets[name]
	return v, ok
}

// Snapshot returns a shallow copy of all secrets. Always non-nil.
func (s *SecretStore) Snapshot() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.secrets))
	maps.Copy(out, s.secrets)
	return out
}

// loadSecretFile reads a .env-formatted file as literal KEY=VALUE pairs.
// Unlike godotenv.Parse, values are NOT subject to $VAR expansion,
// ensuring secret values containing $ are stored verbatim.
func loadSecretFile(path string) (map[string]string, error) {
	expanded, err := expandHome(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(expanded)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseEnvFile(f)
}

// parseEnvFile parses a .env file supporting:
//   - KEY=VALUE (unquoted)
//   - KEY="VALUE" (double-quoted, strips quotes)
//   - KEY='VALUE' (single-quoted, strips quotes)
//   - export KEY=VALUE
//   - # comments and blank lines
//
// Values are treated as literals — no $VAR expansion.
func parseEnvFile(r *os.File) (map[string]string, error) {
	result := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip optional "export " prefix.
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimSpace(line)

		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])

		// Strip matching quotes.
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		result[key] = val
	}
	return result, scanner.Err()
}

// expandHome replaces a leading ~/ (or lone ~) with the user's home directory.
// Only ~/ is expanded; ~username syntax is not supported.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand ~: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}
