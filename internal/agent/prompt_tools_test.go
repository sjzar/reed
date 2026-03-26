package agent

import (
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestBuildToolSummary(t *testing.T) {
	t.Run("empty defs returns empty", func(t *testing.T) {
		got := buildToolSummary(nil)
		if got != "" {
			t.Errorf("expected empty, got %q", got)
		}
		got = buildToolSummary([]model.ToolDef{})
		if got != "" {
			t.Errorf("expected empty for empty slice, got %q", got)
		}
	})

	t.Run("single tool with description", func(t *testing.T) {
		defs := []model.ToolDef{
			{Name: "read_file", Description: "Read the contents of a file"},
		}
		got := buildToolSummary(defs)
		if !strings.Contains(got, "## Available Tools") {
			t.Error("missing header")
		}
		if !strings.Contains(got, "- read_file: Read the contents of a file") {
			t.Errorf("missing tool entry in:\n%s", got)
		}
	})

	t.Run("tool without description", func(t *testing.T) {
		defs := []model.ToolDef{
			{Name: "noop"},
		}
		got := buildToolSummary(defs)
		if !strings.Contains(got, "- noop\n") {
			t.Errorf("expected bare name, got:\n%s", got)
		}
	})

	t.Run("multiple tools", func(t *testing.T) {
		defs := []model.ToolDef{
			{Name: "read_file", Description: "Read file"},
			{Name: "write_file", Description: "Write file"},
		}
		got := buildToolSummary(defs)
		if !strings.Contains(got, "- read_file: Read file") {
			t.Error("missing read_file")
		}
		if !strings.Contains(got, "- write_file: Write file") {
			t.Error("missing write_file")
		}
	})
}
