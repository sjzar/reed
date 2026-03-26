package workflow

import (
	"strings"
	"testing"
)

func TestLoadBase_RejectRegistryRef(t *testing.T) {
	for _, src := range []string{
		"agent/document-expert@v1",
		"github.com/foo/bar.yml@ref",
	} {
		_, err := LoadBase(src)
		if err == nil {
			t.Errorf("LoadBase(%q) should fail for registry ref", src)
			continue
		}
		if !strings.Contains(err.Error(), "registry references not supported") {
			t.Errorf("LoadBase(%q) error = %v, want registry rejection", src, err)
		}
	}
}

func TestLoadBase_RejectMissingFile(t *testing.T) {
	_, err := LoadBase("/nonexistent/path/workflow.yml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadSetFile_RejectURL(t *testing.T) {
	_, err := LoadSetFile("https://example.com/patch.yml")
	if err == nil {
		t.Fatal("expected error for URL set-file")
	}
	if !strings.Contains(err.Error(), "set-file must be a local file") {
		t.Errorf("unexpected error: %v", err)
	}
}
