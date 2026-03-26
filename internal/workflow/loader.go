package workflow

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RawWorkflow is the untyped YAML representation used for merging.
// All fields are kept as map[string]any to support RFC 7386 merge.
type RawWorkflow = map[string]any

// LoadBase loads a base workflow from a local path or HTTP/HTTPS URL.
// Registry-style references (containing @) are rejected.
func LoadBase(source string) (RawWorkflow, error) {
	if strings.Contains(source, "@") {
		return nil, fmt.Errorf("registry references not supported: %s", source)
	}
	if isURL(source) {
		return loadHTTP(source)
	}
	return loadFile(source)
}

// LoadSetFile loads a set-file patch. Only local paths are allowed.
func LoadSetFile(path string) (RawWorkflow, error) {
	if isURL(path) {
		return nil, fmt.Errorf("set-file must be a local file, got URL: %s", path)
	}
	return loadFile(path)
}

func loadFile(path string) (RawWorkflow, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", path, err)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", absPath, err)
	}
	return parseContent(data)
}

func loadHTTP(rawURL string) (RawWorkflow, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %q: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %q: HTTP %d", rawURL, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20+1)) // 10MB limit
	if err != nil {
		return nil, fmt.Errorf("read response from %q: %w", rawURL, err)
	}
	if len(data) > 10<<20 {
		return nil, fmt.Errorf("workflow from %q exceeds 10MB size limit", rawURL)
	}
	return parseContent(data)
}

// ParseBytes parses raw YAML bytes into a RawWorkflow.
func ParseBytes(data []byte) (RawWorkflow, error) {
	return parseContent(data)
}

func parseContent(data []byte) (RawWorkflow, error) {
	var raw RawWorkflow
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("empty workflow document")
	}
	return raw, nil
}

func isURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")
}
