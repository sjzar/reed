package skill

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

var frontMatterSep = []byte("---")

// ParseFrontMatter parses YAML front matter from a SKILL.md file.
// Returns the parsed metadata, the body after front matter, and any error.
func ParseFrontMatter(data []byte) (SkillMeta, string, error) {
	var meta SkillMeta

	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if !bytes.HasPrefix(trimmed, frontMatterSep) {
		// No front matter — entire content is body
		return meta, string(data), nil
	}

	// Find the closing ---
	rest := trimmed[len(frontMatterSep):]
	// Skip the newline after opening ---
	if idx := bytes.IndexByte(rest, '\n'); idx >= 0 {
		rest = rest[idx+1:]
	}

	closeIdx := bytes.Index(rest, append([]byte("\n"), frontMatterSep...))
	if closeIdx < 0 {
		// Try without leading newline (closing --- at start of remaining)
		if bytes.HasPrefix(rest, frontMatterSep) {
			closeIdx = 0
		} else {
			return meta, "", fmt.Errorf("unclosed front matter: missing closing ---")
		}
	}

	yamlBlock := rest[:closeIdx]
	// Skip past \n---\n to get body after closing separator
	body := rest[closeIdx+1:] // skip the \n before ---
	// Now body starts with "---\n..." — skip past the closing --- line
	if idx := bytes.IndexByte(body, '\n'); idx >= 0 {
		body = body[idx+1:]
	} else {
		body = nil
	}

	if err := yaml.Unmarshal(yamlBlock, &meta); err != nil {
		return meta, "", fmt.Errorf("parse front matter YAML: %w", err)
	}

	return meta, string(body), nil
}
