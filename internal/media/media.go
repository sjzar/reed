// Package media provides media storage and URI resolution for the Reed runtime.
// Media files are stored locally with metadata in SQLite. References use the
// media://<id> URI scheme, which can be resolved to raw bytes on demand.
package media

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/sjzar/reed/pkg/mimetype"
)

// Scheme is the URI scheme for media references.
const Scheme = "media"

// MaxMediaSize is the hard limit for a single media file (20 MiB).
const MaxMediaSize = 20 * 1024 * 1024

// Resolved holds the result of resolving a media URI.
type Resolved struct {
	Data     []byte // raw bytes (always populated for local media)
	MIMEType string
}

// URI returns "media://<id>".
func URI(id string) string { return Scheme + "://" + id }

// ParseURI extracts the ID from "media://<id>". Returns ("", false) if not a media URI.
func ParseURI(uri string) (id string, ok bool) {
	const prefix = Scheme + "://"
	if !strings.HasPrefix(uri, prefix) {
		return "", false
	}
	return uri[len(prefix):], true
}

// IsMediaURI reports whether uri starts with "media://".
func IsMediaURI(uri string) bool { return strings.HasPrefix(uri, Scheme+"://") }

// IsDataURI reports whether uri starts with "data:".
func IsDataURI(uri string) bool { return strings.HasPrefix(uri, "data:") }

// ParseDataURI extracts the MIME type and decoded bytes from a data URI.
// Expected format: data:<mimeType>;base64,<data>
func ParseDataURI(uri string) (mimeType string, data []byte, err error) {
	if !strings.HasPrefix(uri, "data:") {
		return "", nil, fmt.Errorf("not a data URI")
	}
	rest := uri[len("data:"):]

	semicolon := strings.Index(rest, ";")
	if semicolon < 0 {
		return "", nil, fmt.Errorf("malformed data URI: missing semicolon")
	}
	mimeType = rest[:semicolon]
	rest = rest[semicolon+1:]

	if !strings.HasPrefix(rest, "base64,") {
		return "", nil, fmt.Errorf("malformed data URI: expected base64 encoding")
	}
	encoded := rest[len("base64,"):]

	data, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		// Try raw (no padding) base64
		data, err = base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			return "", nil, fmt.Errorf("decode base64: %w", err)
		}
	}
	return mimeType, data, nil
}

// DataURI constructs a data URI from MIME type and raw bytes.
func DataURI(mimeType string, data []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// ExtForMIME returns a file extension (with dot) for the given MIME type.
// Delegates to pkg/mimetype for the canonical mapping.
func ExtForMIME(mimeType string) string {
	return mimetype.ExtFromMIME(mimeType)
}

// IsImageMIME reports whether the MIME type is an image type.
func IsImageMIME(mimeType string) bool {
	return mimetype.IsImage(mimeType)
}
