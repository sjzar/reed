package agent

import (
	"fmt"
	"strings"

	"github.com/sjzar/reed/internal/model"
)

// buildToolSummary formats tool definitions into a concise prompt section.
// Uses Summary field when available, otherwise falls back to the first
// non-empty trimmed line of Description. Full descriptions are sent via
// the tools API parameter — the system prompt only needs short summaries.
// Returns "" when defs is empty.
func buildToolSummary(defs []model.ToolDef) string {
	if len(defs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Available Tools\n")

	hasBash := false
	hasDedicated := false

	for _, d := range defs {
		summary := toolSummaryLine(d)
		if summary != "" {
			fmt.Fprintf(&b, "- %s: %s\n", d.Name, summary)
		} else {
			fmt.Fprintf(&b, "- %s\n", d.Name)
		}

		// Track tool composition for routing guidelines.
		switch d.Name {
		case "bash":
			hasBash = true
		case "read", "write", "edit", "search":
			hasDedicated = true
		}
	}

	// Append routing guidelines only when both bash and dedicated tools are present.
	if hasBash && hasDedicated {
		b.WriteString("\n## Tool Selection Guidelines\n")
		b.WriteString("- Prefer dedicated tools over bash for file and search operations. They have better error handling, security checks, and can run in parallel.\n")
		b.WriteString("- Read a file before editing to ensure old_string matches exactly.\n")
	}

	return b.String()
}

// toolSummaryLine returns a short one-line summary for the tool.
// Prefers the explicit Summary field; falls back to the first non-empty
// trimmed line of Description.
func toolSummaryLine(d model.ToolDef) string {
	if d.Summary != "" {
		return d.Summary
	}
	if d.Description == "" {
		return ""
	}
	for _, line := range strings.Split(d.Description, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}
