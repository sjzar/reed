// Package mimetype provides unified MIME type ↔ file extension mapping
// and content classification for the Reed runtime.
package mimetype

import (
	"net/http"
	"path/filepath"
	"strings"
)

// Well-known MIME ↔ extension mappings.
var (
	extToMIME = map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".webp": "image/webp",
		".svg":  "image/svg+xml",
		".pdf":  "application/pdf",
		".txt":  "text/plain",
		".html": "text/html",
		".htm":  "text/html",
		".css":  "text/css",
		".csv":  "text/csv",
		".json": "application/json",
		".xml":  "application/xml",
		".mp3":  "audio/mpeg",
		".mp4":  "video/mp4",
		".wav":  "audio/wav",
		".zip":  "application/zip",
		".gz":   "application/gzip",
		".tar":  "application/x-tar",
	}

	mimeToExt = map[string]string{
		"image/png":         ".png",
		"image/jpeg":        ".jpg",
		"image/gif":         ".gif",
		"image/webp":        ".webp",
		"image/svg+xml":     ".svg",
		"application/pdf":   ".pdf",
		"text/plain":        ".txt",
		"text/html":         ".html",
		"text/css":          ".css",
		"text/csv":          ".csv",
		"application/json":  ".json",
		"application/xml":   ".xml",
		"text/xml":          ".xml",
		"audio/mpeg":        ".mp3",
		"video/mp4":         ".mp4",
		"audio/wav":         ".wav",
		"application/zip":   ".zip",
		"application/gzip":  ".gz",
		"application/x-tar": ".tar",
	}
)

// FromExt returns the MIME type for a file extension (with dot).
// Returns "application/octet-stream" for unknown extensions.
func FromExt(ext string) string {
	if m, ok := extToMIME[strings.ToLower(ext)]; ok {
		return m
	}
	return "application/octet-stream"
}

// ExtFromMIME returns the file extension (with dot) for a MIME type.
// Returns empty string for unknown MIME types.
func ExtFromMIME(mime string) string {
	if e, ok := mimeToExt[mime]; ok {
		return e
	}
	// Try subtype as extension for simple patterns (e.g., "audio/ogg" → ".ogg")
	if _, sub, ok := strings.Cut(mime, "/"); ok {
		if !strings.Contains(sub, ".") && !strings.Contains(sub, "+") && len(sub) <= 5 {
			return "." + sub
		}
	}
	return ""
}

// IsImage reports whether the MIME type is an image type.
func IsImage(mime string) bool {
	return strings.HasPrefix(mime, "image/")
}

// IsPDF reports whether the MIME type is PDF.
func IsPDF(mime string) bool {
	return mime == "application/pdf"
}

// IsText reports whether the MIME type is a text type.
func IsText(mime string) bool {
	return strings.HasPrefix(mime, "text/")
}

// Detect returns the MIME type for a file, using extension first then content sniffing.
// The data parameter should be the first 512+ bytes of the file for content sniffing.
// If path is empty, only content sniffing is used.
func Detect(path string, data []byte) string {
	// Extension-based detection takes priority (more accurate for known types)
	if path != "" {
		ext := strings.ToLower(filepath.Ext(path))
		if m, ok := extToMIME[ext]; ok {
			return m
		}
	}
	// Content sniffing fallback
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}

// DetectFromPath returns the MIME type based on file extension only.
// Returns "application/octet-stream" for unknown extensions.
func DetectFromPath(path string) string {
	return FromExt(filepath.Ext(path))
}
